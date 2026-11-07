package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerDocuments(mux *http.ServeMux) {
	route(mux, "/v1/projects/{key}/documents", methods{"GET": h.listDocuments})
	route(mux, "/v1/documents", methods{"GET": h.searchDocuments})

	// A document name may hold slashes, so one pattern takes the name and the
	// suffix; a name ends in .md and never in the suffix of a sub-resource.
	byPart := map[string]http.HandlerFunc{
		"":           methods{"GET": h.getDocument, "PUT": h.putDocument}.serve(),
		"/archive":   methods{"POST": h.archiveDocument, "DELETE": h.unarchiveDocument}.serve(),
		"/relations": methods{"POST": h.addRelation, "DELETE": h.removeRelation}.serve(),
		"/activity":  methods{"GET": h.documentActivity}.serve(),
	}
	mux.HandleFunc("/v1/projects/{key}/documents/{name...}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		for _, part := range []string{"/archive", "/relations", "/activity"} {
			if trimmed, ok := strings.CutSuffix(name, part); ok && trimmed != "" {
				r.SetPathValue("name", trimmed)
				byPart[part](w, r)
				return
			}
		}
		byPart[""](w, r)
	})
}

// documentFields are the fields of a put request.
var documentFields = map[string]bool{
	"content": true, "location": true, "kind": true, "date": true, "title": true, "tags": true, "supersedes": true,
	"related": true, "issues": true, "archived": true, "imported": true, "path": true,
}

// putDocument stores a record document: 201 for a new one, 200 for the same
// content and metadata again. Other content under the same name is a 409, and so
// is other metadata for the same content. A null field is a field that was not
// sent.
func (h *handler) putDocument(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, documentFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in store.DocumentInput
	var location *string
	if aerr := firstError(decodeField(b, "content", &in.Content), decodeOptional(b, "location", &location)); aerr != nil {
		writeError(w, aerr)
		return
	}
	args := &in.Args
	if aerr := firstError(
		decodeOptional(b, "kind", &args.Kind), decodeOptional(b, "date", &args.Date),
		decodeOptional(b, "title", &args.Title), decodeOptional(b, "path", &args.Path),
		decodeOptional(b, "archived", &args.Archived), decodeOptional(b, "imported", &args.Imported),
		decodeList(b, "tags", &args.Tags), decodeList(b, "supersedes", &args.Supersedes),
		decodeList(b, "related", &args.Related), decodeList(b, "issues", &args.Issues),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	project, name := r.PathValue("key"), r.PathValue("name")
	if location != nil {
		// A location is checked after the project, as every value is: 404, 409, then 422.
		if err := h.checkProjectActive(r, project); err != nil {
			writeError(w, h.storeError(r, err))
			return
		}
		loc, ok := model.ParseDocumentLocation(*location)
		if !ok {
			writeError(w, validationFailed([]Detail{{Field: "location", Message: "history, archive, legacy 중 하나여야 합니다."}}))
			return
		}
		// A client of v0.8.x sends the content and a location, nothing else.
		in.LocationOnly = !args.HasMetadata()
		args.ApplyLocation(name, loc)
	}
	d, created, err := h.store.PutDocument(r.Context(), TraceFrom(r.Context()), project, name, in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	status := http.StatusOK
	if created {
		w.Header().Set("Location", "/v1/projects/"+project+"/documents/"+name)
		status = http.StatusCreated
	}
	writeJSON(w, status, d)
}

// checkProjectActive reports a project that does not exist or is not active
// with the error the store gives for it.
func (h *handler) checkProjectActive(r *http.Request, key string) error {
	p, err := h.store.GetProject(r.Context(), key)
	if err != nil {
		return err
	}
	if p.Status != model.ProjectActive {
		return store.ErrProjectNotActive
	}
	return nil
}

func firstError(errs ...*apiError) *apiError {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// decodeField decodes a field into dst when it is present and not null.
func decodeField[T any](b body, name string, dst *T) *apiError {
	_, _, aerr := field(b, name, dst)
	return aerr
}

// decodeOptional decodes a field into a new value that dst points to when the
// field is present and not null.
func decodeOptional[T any](b body, name string, dst **T) *apiError {
	var v T
	present, null, aerr := field(b, name, &v)
	if aerr == nil && present && !null {
		*dst = &v
	}
	return aerr
}

// decodeList decodes a list of text into dst when it is present and not null.
// An empty list is a list that was sent; dst is nil otherwise.
func decodeList(b body, name string, dst *[]string) *apiError {
	list := []string{}
	present, null, aerr := field(b, name, &list)
	if aerr == nil && present && !null {
		if list == nil {
			list = []string{}
		}
		*dst = list
	}
	return aerr
}

// archiveDocument and unarchiveDocument mark a record document archived and take
// the mark off. A body, if any, is not read.
func (h *handler) archiveDocument(w http.ResponseWriter, r *http.Request) { h.setArchived(w, r, true) }
func (h *handler) unarchiveDocument(w http.ResponseWriter, r *http.Request) {
	h.setArchived(w, r, false)
}

func (h *handler) setArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	d, err := h.store.SetDocumentArchived(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), r.PathValue("name"), archived)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *handler) addRelation(w http.ResponseWriter, r *http.Request) { h.changeRelation(w, r, true) }
func (h *handler) removeRelation(w http.ResponseWriter, r *http.Request) {
	h.changeRelation(w, r, false)
}

// changeRelation adds or removes the one relation that the body names.
func (h *handler) changeRelation(w http.ResponseWriter, r *http.Request, add bool) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"kind": true, "target": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var kind, target string
	if aerr := firstError(decodeField(b, "kind", &kind), decodeField(b, "target", &target)); aerr != nil {
		writeError(w, aerr)
		return
	}
	var bad []Detail
	rel, ok := model.ParseRelationKind(kind)
	if !ok {
		bad = append(bad, Detail{Field: "kind", Message: "supersedes, related, about 중 하나여야 합니다."})
	}
	if strings.TrimSpace(target) == "" {
		bad = append(bad, Detail{Field: "target", Message: "대상이 필요합니다. supersedes와 related는 문서 이름, about은 이슈 키입니다."})
	}
	if len(bad) > 0 {
		writeError(w, validationFailed(bad))
		return
	}
	d, err := h.store.ChangeDocumentRelation(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), r.PathValue("name"), rel, target, add)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// documentActivity lists the changes of the archive mark and the relations of
// one document, newest first.
func (h *handler) documentActivity(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.ListDocumentActivity(r.Context(), r.PathValue("key"), r.PathValue("name"), limit, offset)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) getDocument(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	d, err := h.store.GetDocument(r.Context(), r.PathValue("key"), r.PathValue("name"))
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 500), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.ListDocuments(r.Context(), r.PathValue("key"), limit, offset)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) searchDocuments(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.DocumentFilter{
		Query:    q.one("q"),
		Projects: q.csv("project"),
		Kinds:    parseEnumList(q, "kind", model.ParseDocumentKind),
		States:   parseStates(q),
		Imported: parseBool(q, "imported"),
	}
	rejectLocation(q)
	for _, p := range f.Projects {
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	if _, err := store.BuildMatch(f.Query); err != nil {
		q.add("q", "검색어는 최대 10항입니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.SearchDocuments(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

// parseStates reads the state filter of a document search: current when none is
// given, and no filter, which is every state, for all.
func parseStates(q *query) []model.DocumentState {
	values := q.csv("state")
	if len(values) == 0 {
		return []model.DocumentState{model.StateCurrent}
	}
	var out []model.DocumentState
	for _, v := range values {
		if v == "all" {
			return nil
		}
		state, ok := model.ParseDocumentState(v)
		if !ok {
			q.add("state", fmt.Sprintf("%q는 허용되지 않는 값입니다. current, superseded, archived, all 중 하나여야 합니다.", v))
			continue
		}
		out = append(out, state)
	}
	return out
}

// parseBool reads an optional true or false parameter.
func parseBool(q *query, name string) *bool {
	v := q.one(name)
	switch v {
	case "":
		return nil
	case "true", "false":
		b := v == "true"
		return &b
	}
	q.add(name, "true 또는 false여야 합니다.")
	return nil
}

// rejectLocation refuses the location filter, which the state and imported
// filters replaced.
func rejectLocation(q *query) {
	if _, ok := q.values["location"]; ok {
		q.seen["location"] = true
		q.add("location", "location은 없어졌습니다. 상태는 state, 가져온 문서는 imported를 쓰세요.")
	}
}
