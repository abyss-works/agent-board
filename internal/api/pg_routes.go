package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wsk0715/agent-board/internal/model"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

func (h *handler) registerPGRoutes(mux *http.ServeMux) {
	// Projects
	route(mux, "/v1/projects", methods{"GET": h.pgListProjects, "POST": h.pgCreateProject})
	route(mux, "/v1/projects/{key}", methods{"GET": h.pgGetProject, "PATCH": h.pgPatchProject, "DELETE": h.pgDeleteProject})

	// Epics
	route(mux, "/v1/projects/{key}/epics", methods{"GET": h.pgListEpics, "POST": h.pgCreateEpic})
	route(mux, "/v1/epics/{key}", methods{"GET": h.pgGetEpic, "PATCH": h.pgPatchEpic, "DELETE": h.pgDeleteEpic})

	// Milestones (three-stage: planned, active, closed + deleted)
	route(mux, "/v1/projects/{key}/milestones", methods{"GET": h.pgListMilestones, "POST": h.pgCreateMilestone})
	route(mux, "/v1/projects/{key}/milestones/{id}", methods{"GET": h.pgGetProjectMilestone, "PATCH": h.pgPatchProjectMilestone, "DELETE": h.pgDeleteProjectMilestone})
	route(mux, "/v1/milestones/{id}", methods{"GET": h.pgGetMilestone, "PATCH": h.pgPatchMilestone, "DELETE": h.pgDeleteMilestone})

	// Issues, comments, activities, claims, verification
	route(mux, "/v1/projects/{key}/issues", methods{"GET": h.pgListProjectIssues, "POST": h.pgCreateProjectIssue})
	route(mux, "/v1/issues/{key}", methods{"GET": h.pgGetIssue, "PATCH": h.pgPatchIssue, "DELETE": h.pgDeleteIssue})
	route(mux, "/v1/issues/{key}/comments", methods{"POST": h.pgAddComment})
	route(mux, "/v1/issues/{key}/activities", methods{"GET": h.pgListIssueActivities})
	route(mux, "/v1/issues/{key}/activity", methods{"GET": h.pgListIssueActivities})
	route(mux, "/v1/issues/{key}/claim", methods{"POST": h.pgClaimIssue, "DELETE": h.pgReleaseClaim})
	route(mux, "/v1/issues/{key}/verify", methods{"POST": h.pgVerifyIssue, "DELETE": h.pgUnverifyIssue})

	// Mutable specs
	route(mux, "/v1/projects/{key}/specs", methods{"GET": h.pgListSpecs})
	route(mux, "/v1/projects/{key}/specs/{slug}", methods{"GET": h.pgGetSpec, "PUT": h.pgUpsertSpec})
	route(mux, "/v1/projects/{key}/specs/{slug}/revisions", methods{"GET": h.pgListSpecRevisions})
	route(mux, "/v1/projects/{key}/specs/{slug}/archive", methods{
		"POST":   h.pgArchiveSpec,
		"DELETE": h.pgUnarchiveSpec,
		"PATCH":  h.pgPatchSpecArchive,
	})

	// Immutable records (name may contain slashes)
	route(mux, "/v1/projects/{key}/records", methods{"GET": h.pgListRecords, "POST": h.pgCreateRecord})
	recByPart := map[string]http.HandlerFunc{
		"":         methods{"GET": h.pgGetRecord}.serve(),
		"/archive": methods{"POST": h.pgArchiveRecord, "DELETE": h.pgUnarchiveRecord, "PATCH": h.pgPatchRecordArchive}.serve(),
	}
	mux.HandleFunc("/v1/projects/{key}/records/{name...}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if trimmed, ok := strings.CutSuffix(name, "/archive"); ok && trimmed != "" {
			r.SetPathValue("name", trimmed)
			recByPart["/archive"](w, r)
			return
		}
		recByPart[""](w, r)
	})

	// Sessions, messages, events, devices, tokens
	route(mux, "/v1/sessions", methods{"GET": h.pgListSessions})
	route(mux, "/v1/sessions/{actor}/{session}", methods{"GET": h.pgGetSession})
	route(mux, "/v1/projects/{key}/messages", methods{"GET": h.pgListMessages, "POST": h.pgCreateMessage})
	route(mux, "/v1/messages/{id}", methods{"DELETE": h.pgDeleteMessage})
	route(mux, "/v1/messages/{id}/ack", methods{"POST": h.pgAckMessage})
	route(mux, "/v1/events", methods{"GET": h.pgListEvents})
	route(mux, streamEventsPattern, methods{"GET": h.pgStreamEvents})
	h.registerDevices(mux)
	route(mux, "/v1/tokens", methods{"GET": h.userOnly(h.pgListTokens), "POST": h.pgExchangeToken})
	route(mux, "/v1/tokens/current", methods{"DELETE": h.pgRevokeCurrentToken})
	route(mux, "/v1/tokens/{id}", methods{"DELETE": h.userOnly(h.pgRevokeToken)})
}

func (h *handler) pgStoreError(r *http.Request, err error) *apiError {
	var verr *model.ValidationError
	var rerr *pgstore.RefError
	var ierr *pgstore.ImmutableError
	var cerr *pgstore.ErrIssueClaimed
	var uerr *pgstore.UnverifiedIssuesError
	var eerr *pgstore.EdgeAlreadyDecidedError

	switch {
	case errors.Is(err, pgstore.ErrNotFound):
		return notFound("대상이 없습니다.")
	case errors.Is(err, pgstore.ErrMilestoneScopeLocked):
		return &apiError{
			Status:  http.StatusUnprocessableEntity,
			Code:    "milestone_scope_locked",
			Message: "진행 중(active) 마일스톤에는 P0 또는 bug 이슈만 편입할 수 있습니다.",
			Details: []Detail{{
				Field:   "milestone_id",
				Reason:  "active_milestone_requires_p0_or_bug",
				Message: "예외 편입이 필요하면 force=true와 reason을 함께 전달하세요.",
			}},
		}
	case errors.Is(err, model.ErrPayloadTooLarge):
		return &apiError{
			Status:  http.StatusRequestEntityTooLarge,
			Code:    "payload_too_large",
			Message: "문서 본문은 2MiB(2097152바이트)를 넘을 수 없습니다.",
			Details: []Detail{{Field: "content", Reason: "content_exceeds_2mib"}},
		}
	case errors.Is(err, model.ErrInvalidUTF8):
		return &apiError{
			Status:  http.StatusUnsupportedMediaType,
			Code:    "unsupported_media_type",
			Message: "UTF-8 텍스트 또는 마크다운 입력만 수용합니다.",
			Details: []Detail{{Field: "content", Reason: "invalid_utf8"}},
		}
	case errors.Is(err, model.ErrBinaryPayload):
		return &apiError{
			Status:  http.StatusUnsupportedMediaType,
			Code:    "unsupported_media_type",
			Message: "UTF-8 텍스트 또는 마크다운 입력만 수용합니다.",
			Details: []Detail{{Field: "content", Reason: "binary_payload"}},
		}
	case errors.As(err, &uerr):
		details := make([]Detail, len(uerr.Issues))
		for i, it := range uerr.Issues {
			msg := it.Key + ": " + it.Title
			if it.VerificationNote != nil && *it.VerificationNote != "" {
				msg = it.Key + ": " + *it.VerificationNote
			}
			details[i] = Detail{Field: "verification", Reason: "unverified", Message: msg}
		}
		return &apiError{
			Status:  http.StatusConflict,
			Code:    "unverified_issues",
			Message: "검증되지 않은 항목이 남아 마일스톤을 닫을 수 없습니다.",
			Details: details,
		}
	case errors.As(err, &eerr):
		return &apiError{
			Status:  http.StatusConflict,
			Code:    "edge_already_decided",
			Message: "이미 결정 처리된 간선입니다.",
			Details: []Detail{{
				Field:   "edge_id",
				Reason:  "already_" + string(eerr.Status),
				Message: fmt.Sprintf("edge %d는 이미 %s 상태입니다.", eerr.EdgeID, eerr.Status),
			}},
		}
	case errors.Is(err, pgstore.ErrEdgeAlreadyDecided):
		return &apiError{
			Status:  http.StatusConflict,
			Code:    "edge_already_decided",
			Message: "이미 결정 처리된 간선입니다.",
		}
	case errors.As(err, &cerr):
		return &apiError{
			Status:  http.StatusConflict,
			Code:    "claimed",
			Message: "다른 세션이 점유 중인 이슈입니다.",
			Details: []Detail{{Field: "claim", Reason: "claimed", Message: cerr.Actor + "/" + cerr.SessionID + " 세션이 점유 중입니다."}},
		}
	case errors.Is(err, pgstore.ErrClaimForbidden):
		return forbidden("다른 세션의 점유는 해제할 수 없습니다.",
			Detail{Field: "claim", Reason: "not_claim_holder", Message: "점유한 세션 본인이나 사용자 행위자만 점유를 해제할 수 있습니다."})
	case errors.As(err, &ierr):
		f := "content"
		if ierr.Field != "" {
			f = ierr.Field
		}
		return &apiError{
			Status:  http.StatusConflict,
			Code:    "conflict",
			Message: "같은 이름의 기록 문서가 다른 내용으로 이미 있습니다. 기록 문서는 고칠 수 없습니다.",
			Details: []Detail{{
				Field:   f,
				Reason:  "immutable",
				Message: "문서 " + ierr.Name + "에 저장된 내용의 sha256은 " + ierr.SHA256 + "입니다.",
				SHA256:  ierr.SHA256,
			}},
		}
	case errors.Is(err, pgstore.ErrDuplicateKey):
		return conflict("duplicate_key", "같은 키가 이미 있습니다.")
	case errors.Is(err, pgstore.ErrDuplicateMilestoneName):
		return &apiError{
			Status:  http.StatusConflict,
			Code:    "conflict",
			Message: "같은 이름의 삭제되지 않은 마일스톤이 이미 있습니다.",
			Details: []Detail{{Field: "name", Reason: "duplicate"}},
		}
	case errors.Is(err, pgstore.ErrGlobalProjectProtected):
		return conflict("protected", "GLOBAL 전역 프로젝트는 삭제할 수 없습니다.")
	case errors.Is(err, pgstore.ErrProjectNotActive):
		return conflict("project_not_active", "활성 상태가 아닌 프로젝트에는 쓸 수 없습니다.")
	case errors.Is(err, pgstore.ErrVersionMismatch):
		return errPreconditionFailed
	case errors.As(err, &rerr):
		return validationFailed([]Detail{{Field: rerr.Field, Reason: "not_found", Message: rerr.Key + " 대상을 찾지 못했습니다."}})
	case errors.As(err, &verr):
		details := make([]Detail, len(verr.Fields))
		for i, f := range verr.Fields {
			details[i] = Detail{Field: f.Field, Reason: f.Reason, Message: f.Message}
		}
		return validationFailed(details)
	}
	h.opts.Logger.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
	return errInternal
}

// readDocumentUTF8Body validates Content-Type, checks raw bytes for strict UTF-8
// and NUL bytes prior to JSON unmarshaling (because encoding/json replaces
// invalid UTF-8 with U+FFFD), and enforces the document body size ceiling.
func readDocumentUTF8Body(w http.ResponseWriter, r *http.Request, allowed map[string]bool) (body, *apiError) {
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || mt != "application/json" {
			return nil, &apiError{
				Status:  http.StatusUnsupportedMediaType,
				Code:    "unsupported_media_type",
				Message: "application/json UTF-8 입력만 수용합니다.",
				Details: []Detail{{Field: "Content-Type", Reason: "unsupported_content_type"}},
			}
		}
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDocumentBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, &apiError{
				Status:  http.StatusRequestEntityTooLarge,
				Code:    "payload_too_large",
				Message: "문서 본문은 2MiB(2097152바이트)를 넘을 수 없습니다.",
				Details: []Detail{{Field: "content", Reason: "content_exceeds_2mib"}},
			}
		}
		return nil, badRequest("요청 본문을 읽지 못했습니다.")
	}
	if !utf8.Valid(raw) {
		return nil, &apiError{
			Status:  http.StatusUnsupportedMediaType,
			Code:    "unsupported_media_type",
			Message: "UTF-8 텍스트 또는 마크다운 입력만 수용합니다.",
			Details: []Detail{{Field: "content", Reason: "invalid_utf8"}},
		}
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return nil, &apiError{
			Status:  http.StatusUnsupportedMediaType,
			Code:    "unsupported_media_type",
			Message: "UTF-8 텍스트 또는 마크다운 입력만 수용합니다.",
			Details: []Detail{{Field: "content", Reason: "binary_payload"}},
		}
	}
	return decodeBodyObject(raw, allowed)
}

// --- Projects ---

func (h *handler) pgListProjects(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	var statuses []model.ProjectStatus
	switch param := q.one("status"); param {
	case "":
		statuses = []model.ProjectStatus{model.ProjectActive}
	case "all":
		statuses = model.ProjectStatuses
	default:
		statuses = parseEnumList(q, "status", model.ParseProjectStatus)
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListProjects(r.Context(), statuses)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (h *handler) pgCreateProject(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"key": true, "name": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateProjectInput
	if aerr := firstError(decodeField(b, "key", &in.Key), decodeField(b, "name", &in.Name)); aerr != nil {
		writeError(w, aerr)
		return
	}
	p, err := h.pgStore.CreateProject(r.Context(), TraceFrom(r.Context()), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/projects/"+p.Key)
	writeJSON(w, http.StatusCreated, p)
}

func (h *handler) pgGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := h.pgStore.GetProject(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handler) pgPatchProject(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"name": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch pgstore.ProjectPatch
	var name string
	if present, null, err := field(b, "name", &name); err != nil {
		writeError(w, err)
		return
	} else if present {
		if null {
			writeError(w, validationFailed([]Detail{{Field: "name", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Name = &name
	}
	var status model.ProjectStatus
	if present, null, err := field(b, "status", &status); err != nil {
		writeError(w, err)
		return
	} else if present {
		if null {
			writeError(w, validationFailed([]Detail{{Field: "status", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Status = &status
	}
	p, err := h.pgStore.UpdateProject(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), patch)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handler) pgDeleteProject(w http.ResponseWriter, r *http.Request) {
	p, err := h.pgStore.DeleteProject(r.Context(), TraceFrom(r.Context()), r.PathValue("key"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// --- Epics ---

func (h *handler) pgListEpics(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.EpicFilter{Limit: q.limit(50, 200), Offset: q.offset()}
	switch param := q.one("status"); param {
	case "":
		f.Statuses = []model.EpicStatus{model.EpicOpen}
	case "all":
		f.Statuses = model.EpicStatuses
	default:
		f.Statuses = parseEnumList(q, "status", model.ParseEpicStatus)
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListEpics(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (h *handler) pgCreateEpic(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{
		"key": true, "title": true, "description": true, "source_issue_id": true, "source_issue_key": true,
	})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateEpicInput
	if aerr := firstError(
		decodeField(b, "key", &in.Key),
		decodeField(b, "title", &in.Title),
		decodeField(b, "description", &in.Description),
		decodeOptional(b, "source_issue_id", &in.SourceIssueID),
		decodeOptional(b, "source_issue_key", &in.SourceIssueKey),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	ep, err := h.pgStore.CreateEpic(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/epics/"+ep.Key)
	writeJSON(w, http.StatusCreated, ep)
}

func (h *handler) pgGetEpic(w http.ResponseWriter, r *http.Request) {
	ep, err := h.pgStore.GetEpic(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (h *handler) pgPatchEpic(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"title": true, "description": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch pgstore.EpicPatch
	var title, desc string
	var status model.EpicStatus
	if p, n, err := field(b, "title", &title); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "title", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Title = &title
	}
	if p, n, err := field(b, "description", &desc); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "description", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Description = &desc
	}
	if p, n, err := field(b, "status", &status); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "status", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Status = &status
	}
	ep, err := h.pgStore.UpdateEpic(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), patch)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (h *handler) pgDeleteEpic(w http.ResponseWriter, r *http.Request) {
	ep, err := h.pgStore.DeleteEpic(r.Context(), TraceFrom(r.Context()), r.PathValue("key"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

// --- Milestones ---

func (h *handler) pgListMilestones(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	param := q.one("status")
	var statuses []model.MilestoneStatus
	if param == "" {
		statuses = []model.MilestoneStatus{model.MilestonePlanned, model.MilestoneActive}
	} else {
		var err error
		statuses, err = model.ExpandMilestoneStageStatuses(param)
		if err != nil {
			q.add("status", "planned, active, closed, deleted, all 중 하나이거나 쉼표로 이은 목록이어야 합니다.")
		}
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListMilestones(r.Context(), r.PathValue("key"), statuses)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) pgCreateMilestone(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"name": true, "description": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateMilestoneInput
	if aerr := firstError(
		decodeField(b, "name", &in.Name),
		decodeField(b, "description", &in.Description),
		decodeField(b, "status", &in.Status),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.pgStore.CreateMilestone(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/milestones/%d", m.ID))
	writeJSON(w, http.StatusCreated, m)
}

func (h *handler) pgGetMilestone(w http.ResponseWriter, r *http.Request) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.pgStore.GetMilestone(r.Context(), id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) pgGetProjectMilestone(w http.ResponseWriter, r *http.Request) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.pgStore.GetMilestone(r.Context(), id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	if m.Project != model.NormalizeProjectKey(r.PathValue("key")) {
		writeError(w, notFound("대상이 없습니다."))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) pgPatchMilestoneCommon(w http.ResponseWriter, r *http.Request, projectScope string) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if projectScope != "" {
		m, err := h.pgStore.GetMilestone(r.Context(), id)
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		if m.Project != model.NormalizeProjectKey(projectScope) {
			writeError(w, notFound("대상이 없습니다."))
			return
		}
	}
	b, aerr := readBody(w, r, map[string]bool{"name": true, "description": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch pgstore.MilestonePatch
	var name, desc string
	var status model.MilestoneStatus
	if p, n, err := field(b, "name", &name); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "name", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Name = &name
	}
	if p, n, err := field(b, "description", &desc); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "description", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Description = &desc
	}
	if p, n, err := field(b, "status", &status); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "status", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Status = &status
	}
	m, err := h.pgStore.UpdateMilestone(r.Context(), TraceFrom(r.Context()), id, patch)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) pgPatchMilestone(w http.ResponseWriter, r *http.Request) {
	h.pgPatchMilestoneCommon(w, r, "")
}

func (h *handler) pgPatchProjectMilestone(w http.ResponseWriter, r *http.Request) {
	h.pgPatchMilestoneCommon(w, r, r.PathValue("key"))
}

func (h *handler) pgDeleteMilestoneCommon(w http.ResponseWriter, r *http.Request, projectScope string) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if projectScope != "" {
		m, err := h.pgStore.GetMilestone(r.Context(), id)
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		if m.Project != model.NormalizeProjectKey(projectScope) {
			writeError(w, notFound("대상이 없습니다."))
			return
		}
	}
	m, err := h.pgStore.DeleteMilestone(r.Context(), TraceFrom(r.Context()), id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) pgDeleteMilestone(w http.ResponseWriter, r *http.Request) {
	h.pgDeleteMilestoneCommon(w, r, "")
}

func (h *handler) pgDeleteProjectMilestone(w http.ResponseWriter, r *http.Request) {
	h.pgDeleteMilestoneCommon(w, r, r.PathValue("key"))
}

// --- Issues ---

var pgIssueCreateFields = map[string]bool{
	"title": true, "body": true, "type": true, "status": true, "priority": true,
	"epic_id": true, "milestone_id": true, "milestone": true,
	"labels": true, "refs": true, "recheck_condition": true, "decision_needed": true,
	"verification_state": true, "verification_note": true,
	"force": true, "reason": true,
}

var pgIssuePatchFields = withFields(pgIssueCreateFields, "status_reason")

func (h *handler) pgCreateProjectIssue(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, pgIssueCreateFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateIssueInput
	var msAlias *int64
	if aerr := firstError(
		decodeField(b, "title", &in.Title),
		decodeField(b, "body", &in.Body),
		decodeField(b, "type", &in.Type),
		decodeField(b, "status", &in.Status),
		decodeField(b, "priority", &in.Priority),
		decodeOptional(b, "epic_id", &in.EpicID),
		decodeOptional(b, "milestone_id", &in.MilestoneID),
		decodeOptional(b, "milestone", &msAlias),
		decodeList(b, "labels", &in.Labels),
		decodeList(b, "refs", &in.Refs),
		decodeOptional(b, "recheck_condition", &in.RecheckCondition),
		decodeOptional(b, "decision_needed", &in.DecisionNeeded),
		decodeField(b, "verification_state", &in.VerificationState),
		decodeOptional(b, "verification_note", &in.VerificationNote),
		decodeField(b, "force", &in.Force),
		decodeField(b, "reason", &in.Reason),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	if in.MilestoneID == nil && msAlias != nil {
		in.MilestoneID = msAlias
	}
	res, err := h.pgStore.CreateIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/issues/"+res.Issue.Key)
	w.Header().Set("ETag", etag(res.Issue.Version))
	writeJSON(w, http.StatusCreated, res.Issue)
}

func (h *handler) pgListProjectIssues(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r, "label")
	f := pgstore.IssueFilter{
		Types:              parseEnumList(q, "type", model.ParseIssueType),
		Priorities:         parseEnumList(q, "priority", model.ParsePriority),
		VerificationStates: parseEnumList(q, "verification", model.ParseVerificationState),
		Labels:             q.repeated("label"),
		Limit:              q.limit(50, 200),
		Offset:             q.offset(),
	}
	statusParam := q.one("status")
	if statusParam == "" {
		statusParam = "open"
	}
	statuses, err := model.ExpandIssueStatuses(statusParam)
	if err != nil {
		q.add("status", "상태 또는 open, closed, all이어야 합니다.")
	}
	f.Statuses = statuses
	if v := q.one("epic_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			q.add("epic_id", "1 이상의 정수여야 합니다.")
		} else {
			f.EpicID = &id
		}
	}
	if v := q.one("milestone_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			q.add("milestone_id", "1 이상의 정수여야 합니다.")
		} else {
			f.MilestoneID = &id
		}
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListIssues(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, len(items), f.Limit, f.Offset))
}

func (h *handler) pgGetIssue(w http.ResponseWriter, r *http.Request) {
	it, err := h.pgStore.GetIssue(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusOK, it)
}

func (h *handler) pgPatchIssue(w http.ResponseWriter, r *http.Request) {
	version, aerr := ifMatch(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, pgIssuePatchFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch pgstore.IssuePatch
	var title, bodyText, statusReason, recheck, decision, verNote string
	var typ model.IssueType
	var status model.IssueStatus
	var prio model.Priority
	var verState model.VerificationState
	var labels, refs []string
	var epicID, milestoneID int64

	if aerr := firstError(
		decodeOptional(b, "title", &patch.Title),
		decodeOptional(b, "body", &patch.Body),
		decodeOptional(b, "type", &patch.Type),
		decodeOptional(b, "status", &patch.Status),
		decodeOptional(b, "priority", &patch.Priority),
		decodeOptional(b, "status_reason", &patch.StatusReason),
		decodeOptional(b, "verification_state", &patch.VerificationState),
		decodeField(b, "force", &patch.Force),
		decodeField(b, "reason", &patch.Reason),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	_, _, _ = title, bodyText, statusReason
	_, _, _, _ = typ, status, prio, verState

	if p, n, err := field(b, "labels", &labels); err != nil {
		writeError(w, err)
		return
	} else if p && !n {
		patch.Labels = &labels
	}
	if p, n, err := field(b, "refs", &refs); err != nil {
		writeError(w, err)
		return
	} else if p && !n {
		patch.Refs = &refs
	}
	if p, n, err := field(b, "epic_id", &epicID); err != nil {
		writeError(w, err)
		return
	} else if p {
		patch.EpicID.Set = true
		if !n {
			v := epicID
			patch.EpicID.Value = &v
		}
	}
	for _, key := range []string{"milestone_id", "milestone"} {
		if p, n, err := field(b, key, &milestoneID); err != nil {
			writeError(w, err)
			return
		} else if p {
			patch.MilestoneID.Set = true
			if !n {
				v := milestoneID
				patch.MilestoneID.Value = &v
			}
			break
		}
	}
	if p, n, err := field(b, "recheck_condition", &recheck); err != nil {
		writeError(w, err)
		return
	} else if p {
		patch.RecheckCondition.Set = true
		if !n {
			v := recheck
			patch.RecheckCondition.Value = &v
		}
	}
	if p, n, err := field(b, "decision_needed", &decision); err != nil {
		writeError(w, err)
		return
	} else if p {
		patch.DecisionNeeded.Set = true
		if !n {
			v := decision
			patch.DecisionNeeded.Value = &v
		}
	}
	if p, n, err := field(b, "verification_note", &verNote); err != nil {
		writeError(w, err)
		return
	} else if p {
		patch.VerificationNote.Set = true
		if !n {
			v := verNote
			patch.VerificationNote.Value = &v
		}
	}

	res, err := h.pgStore.UpdateIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), version, patch)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("ETag", etag(res.Issue.Version))
	writeJSON(w, http.StatusOK, res.Issue)
}

func (h *handler) pgDeleteIssue(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	reason := q.one("reason")
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	it, err := h.pgStore.DeleteIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), reason)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusOK, it)
}

func (h *handler) pgVerifyIssue(w http.ResponseWriter, r *http.Request) {
	h.pgSetVerification(w, r, model.VerificationVerified)
}

func (h *handler) pgUnverifyIssue(w http.ResponseWriter, r *http.Request) {
	h.pgSetVerification(w, r, model.VerificationPending)
}

func (h *handler) pgSetVerification(w http.ResponseWriter, r *http.Request, st model.VerificationState) {
	version, aerr := ifMatch(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	res, err := h.pgStore.UpdateIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), version, pgstore.IssuePatch{
		VerificationState: &st,
	})
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("ETag", etag(res.Issue.Version))
	writeJSON(w, http.StatusOK, res.Issue)
}

func (h *handler) pgAddComment(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"body": true, "summary": true, "comment_kind": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.AddCommentInput
	var kindText string
	if aerr := firstError(
		decodeField(b, "body", &in.Body),
		decodeOptional(b, "summary", &in.Summary),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	if p, n, err := field(b, "comment_kind", &kindText); err != nil {
		writeError(w, err)
		return
	} else if p && !n {
		k, ok := model.ParseCommentKind(kindText)
		if !ok {
			writeError(w, validationFailed([]Detail{{Field: "comment_kind", Reason: "invalid_value", Message: "decision, finding, lesson 중 하나여야 합니다."}}))
			return
		}
		in.CommentKind = &k
	}
	res, err := h.pgStore.AddComment(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusCreated, res.Activity)
}

func (h *handler) pgListIssueActivities(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.ListIssueActivities(r.Context(), r.PathValue("key"), limit, offset)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) pgClaimIssue(w http.ResponseWriter, r *http.Request) {
	b, aerr := readOptionalBody(w, r, map[string]bool{"reason": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var reason string
	if err := decodeField(b, "reason", &reason); err != nil {
		writeError(w, err)
		return
	}
	claim, created, err := h.pgStore.ClaimIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), reason)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, claim)
}

func (h *handler) pgReleaseClaim(w http.ResponseWriter, r *http.Request) {
	b, aerr := readOptionalBody(w, r, map[string]bool{"outcome": true, "reason": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var outcome string
	var reason *string
	if aerr := firstError(decodeField(b, "outcome", &outcome), decodeOptional(b, "reason", &reason)); aerr != nil {
		writeError(w, aerr)
		return
	}
	claim, err := h.pgStore.ReleaseIssueClaim(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), outcome, reason)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, claim)
}

// --- Mutable Specs ---

func (h *handler) pgUpsertSpec(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	_ = parseBool(q, "wait")
	_ = q.intRange("wait_timeout_ms", 15000, 100, 30000)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readDocumentUTF8Body(w, r, map[string]bool{
		"title": true, "kind": true, "tags": true, "content": true, "change_summary": true,
	})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.UpsertSpecInput
	if aerr := firstError(
		decodeField(b, "title", &in.Title),
		decodeField(b, "kind", &in.Kind),
		decodeList(b, "tags", &in.Tags),
		decodeField(b, "content", &in.Content),
		decodeField(b, "change_summary", &in.ChangeSummary),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	res, err := h.pgStore.UpsertSpec(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), r.PathValue("slug"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
		w.Header().Set("Location", "/v1/projects/"+res.Spec.Project+"/specs/"+res.Spec.Slug)
	}
	writeJSON(w, status, map[string]any{
		"id":                  res.Spec.ID,
		"project":             res.Spec.Project,
		"slug":                res.Spec.Slug,
		"kind":                res.Spec.Kind,
		"title":               res.Spec.Title,
		"tags":                res.Spec.Tags,
		"current_revision_no": res.Spec.CurrentRevisionNo,
		"sha256":              res.Spec.SHA256,
		"size_bytes":          res.Spec.SizeBytes,
		"archived":            res.Spec.Archived,
		"ingest_job_id":       res.IngestJobID,
		"ingest_status":       res.IngestStatus,
		"updated_at":          res.Spec.UpdatedAt,
	})
}

func (h *handler) pgGetSpec(w http.ResponseWriter, r *http.Request) {
	doc, err := h.pgStore.GetSpec(r.Context(), r.PathValue("key"), r.PathValue("slug"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *handler) pgListSpecs(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.SpecFilter{
		Kinds:  q.csv("kind"),
		Limit:  q.limit(50, 200),
		Offset: q.offset(),
	}
	if inc := parseBool(q, "include_archived"); inc != nil {
		f.IncludeArchived = *inc
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListSpecs(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, len(items), f.Limit, f.Offset))
}

func (h *handler) pgListSpecRevisions(w http.ResponseWriter, r *http.Request) {
	items, err := h.pgStore.ListSpecRevisions(r.Context(), r.PathValue("key"), r.PathValue("slug"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (h *handler) pgArchiveSpec(w http.ResponseWriter, r *http.Request) {
	h.pgSetSpecArchived(w, r, true)
}

func (h *handler) pgUnarchiveSpec(w http.ResponseWriter, r *http.Request) {
	h.pgSetSpecArchived(w, r, false)
}

func (h *handler) pgPatchSpecArchive(w http.ResponseWriter, r *http.Request) {
	b, aerr := readOptionalBody(w, r, map[string]bool{"archived": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	archived := true
	if err := decodeField(b, "archived", &archived); err != nil {
		writeError(w, err)
		return
	}
	h.pgSetSpecArchived(w, r, archived)
}

func (h *handler) pgSetSpecArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	doc, err := h.pgStore.SetSpecArchived(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), r.PathValue("slug"), archived)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// --- Immutable Records ---

func (h *handler) pgCreateRecord(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	_ = parseBool(q, "wait")
	_ = q.intRange("wait_timeout_ms", 15000, 100, 30000)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readDocumentUTF8Body(w, r, map[string]bool{
		"name": true, "kind": true, "title": true, "tags": true,
		"recorded_at": true, "archived": true, "imported": true, "source_path": true, "content": true,
	})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateRecordInput
	var recAtStr *string
	if aerr := firstError(
		decodeField(b, "name", &in.Name),
		decodeField(b, "kind", &in.Kind),
		decodeField(b, "title", &in.Title),
		decodeList(b, "tags", &in.Tags),
		decodeOptional(b, "recorded_at", &recAtStr),
		decodeField(b, "archived", &in.Archived),
		decodeField(b, "imported", &in.Imported),
		decodeOptional(b, "source_path", &in.SourcePath),
		decodeField(b, "content", &in.Content),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	if recAtStr != nil {
		t, err := model.ParseTime(*recAtStr)
		if err != nil {
			writeError(w, validationFailed([]Detail{{Field: "recorded_at", Reason: "invalid_value", Message: "RFC 3339 시각이어야 합니다."}}))
			return
		}
		in.RecordedAt = &t
	}
	res, err := h.pgStore.CreateRecord(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
		w.Header().Set("Location", "/v1/projects/"+res.Record.Project+"/records/"+res.Record.Name)
	}
	writeJSON(w, status, map[string]any{
		"id":                    res.Record.ID,
		"project":               res.Record.Project,
		"name":                  res.Record.Name,
		"kind":                  res.Record.Kind,
		"title":                 res.Record.Title,
		"tags":                  res.Record.Tags,
		"sha256":                res.Record.SHA256,
		"size_bytes":            res.Record.SizeBytes,
		"archived":              res.Record.Archived,
		"ingest_job_id":         res.IngestJobID,
		"ingest_status":         res.IngestStatus,
		"supersedes_candidates": res.SupersedesCandidates,
		"created_at":            res.Record.CreatedAt,
	})
}

func (h *handler) pgGetRecord(w http.ResponseWriter, r *http.Request) {
	doc, err := h.pgStore.GetRecord(r.Context(), r.PathValue("key"), r.PathValue("name"))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *handler) pgListRecords(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.RecordFilter{
		Kinds:  q.csv("kind"),
		Limit:  q.limit(50, 200),
		Offset: q.offset(),
	}
	if inc := parseBool(q, "include_archived"); inc != nil {
		f.IncludeArchived = *inc
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListRecords(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, len(items), f.Limit, f.Offset))
}

func (h *handler) pgArchiveRecord(w http.ResponseWriter, r *http.Request) {
	h.pgSetRecordArchived(w, r, true)
}

func (h *handler) pgUnarchiveRecord(w http.ResponseWriter, r *http.Request) {
	h.pgSetRecordArchived(w, r, false)
}

func (h *handler) pgPatchRecordArchive(w http.ResponseWriter, r *http.Request) {
	b, aerr := readOptionalBody(w, r, map[string]bool{"archived": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	archived := true
	if err := decodeField(b, "archived", &archived); err != nil {
		writeError(w, err)
		return
	}
	h.pgSetRecordArchived(w, r, archived)
}

func (h *handler) pgSetRecordArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	doc, err := h.pgStore.SetRecordArchived(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), r.PathValue("name"), archived)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// --- Sessions, Messages, Events, Tokens ---

func (h *handler) pgListSessions(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.ListSessions(r.Context(), limit, offset)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) pgGetSession(w http.ResponseWriter, r *http.Request) {
	ref, aerr := sessionPath(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	s, err := h.pgStore.GetSession(r.Context(), ref.Actor, ref.SessionID)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handler) pgListMessages(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.MessageFilter{
		To:       q.one("to"),
		ToRole:   q.one("to_role"),
		UnreadBy: q.one("unread_by"),
		Limit:    q.limit(50, 200),
		Offset:   q.offset(),
	}
	if all := parseBool(q, "all"); all != nil {
		f.All = *all
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListMessages(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, len(items), f.Limit, f.Offset))
}

func (h *handler) pgCreateMessage(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{
		"to": true, "to_role": true, "issue": true, "reply_to": true, "title": true, "body": true, "ttl": true,
	})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateMessageInput
	if aerr := firstError(
		decodeField(b, "to", &in.To),
		decodeField(b, "to_role", &in.ToRole),
		decodeField(b, "issue", &in.Issue),
		decodeOptional(b, "reply_to", &in.ReplyTo),
		decodeField(b, "title", &in.Title),
		decodeField(b, "body", &in.Body),
		decodeField(b, "ttl", &in.TTL),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.pgStore.CreateMessage(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/messages/%d", m.ID))
	writeJSON(w, http.StatusCreated, m)
}

func (h *handler) pgDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id, aerr := messageID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.pgStore.DeleteMessage(r.Context(), TraceFrom(r.Context()), id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) pgAckMessage(w http.ResponseWriter, r *http.Request) {
	id, aerr := messageID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"reader": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var reader string
	if err := decodeField(b, "reader", &reader); err != nil {
		writeError(w, err)
		return
	}
	read, err := h.pgStore.MarkMessageRead(r.Context(), TraceFrom(r.Context()), id, reader)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, read)
}

func (h *handler) pgListEvents(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	var after int64
	if raw := q.one("after"); raw != "" {
		n, ok := parseNonNegativeInt64(raw)
		if !ok {
			q.add("after", "0 이상의 정수여야 합니다.")
		} else {
			after = n
		}
	}
	project := q.one("project")
	kinds := q.csv("kind")
	limit := q.limit(100, 500)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, nextAfter, err := h.pgStore.ListEvents(r.Context(), pgstore.EventFilter{
		After:   after,
		Project: project,
		Kinds:   kinds,
		Limit:   limit,
	})
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_after": nextAfter})
}

func (h *handler) pgStreamEvents(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	afterRaw := q.one("after")
	var cursor int64
	hasCursor := false
	if afterRaw != "" {
		n, ok := parseNonNegativeInt64(afterRaw)
		if !ok {
			q.add("after", "0 이상의 정수여야 합니다.")
		} else {
			cursor = n
			hasCursor = true
		}
	}
	project := q.one("project")
	kinds := q.csv("kind")
	if lastRaw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastRaw != "" {
		n, ok := parseNonNegativeInt64(lastRaw)
		if !ok {
			q.details = append(q.details, Detail{Field: "Last-Event-ID", Reason: "invalid_header", Message: "0 이상의 정수여야 합니다."})
		} else {
			cursor = n
			hasCursor = true
		}
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	actor := TraceFrom(r.Context()).Actor
	cred := credentialFrom(r.Context())
	sub, unsub, err := h.broker.Subscribe(actor, cred.tokenID)
	if err != nil {
		writeError(w, tooManyRequests("동시 이벤트 스트림 연결 제한을 넘었습니다."))
		return
	}
	defer unsub()
	if !hasCursor {
		latest, err := h.pgStore.LatestEventID(r.Context())
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		cursor = latest
	}
	initialItems, initialNext, err := h.pgStore.ListEvents(r.Context(), pgstore.EventFilter{
		After: cursor, Project: project, Kinds: kinds, Limit: 500,
	})
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	h.stats.streamOpened(r.Method, streamEventsPattern)
	defer h.stats.streamClosed(r.Method, streamEventsPattern)

	for _, ev := range initialItems {
		if err := writeSSEEvent(w, ev); err != nil {
			return
		}
		cursor = ev.ID
	}
	if initialNext > cursor {
		cursor = initialNext
	}
	_ = rc.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.broker.Done():
			return
		case <-sub.closed:
			return
		case <-sub.notify:
			items, nextAfter, err := h.pgStore.ListEvents(r.Context(), pgstore.EventFilter{
				After: cursor, Project: project, Kinds: kinds, Limit: 500,
			})
			if err != nil {
				return
			}
			for _, ev := range items {
				if err := writeSSEEvent(w, ev); err != nil {
					return
				}
				cursor = ev.ID
			}
			if nextAfter > cursor {
				cursor = nextAfter
			}
			_ = rc.Flush()
		}
	}
}

func (h *handler) pgExchangeToken(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"device_code": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var code string
	if err := decodeField(b, "device_code", &code); err != nil {
		writeError(w, err)
		return
	}
	if code == "" {
		writeError(w, validationFailed([]Detail{{Field: "device_code", Reason: "required", Message: "필수입니다."}}))
		return
	}
	d, err := h.devices.take(code)
	switch {
	case errors.Is(err, errDevicePending):
		writeError(w, errAuthorizationPending)
		return
	case errors.Is(err, errDeviceDenied):
		writeError(w, errAccessDenied)
		return
	case errors.Is(err, errDeviceNotFound):
		writeError(w, errExpiredToken)
		return
	case err != nil:
		writeError(w, h.deviceError(r, err))
		return
	}
	secret, err := h.newTokenSecret()
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	tok, revokedIDs, err := h.pgStore.CreateTokenReplacing(r.Context(), TraceFrom(r.Context()), pgstore.CreateTokenInput{
		Actor:           d.actor,
		ClientHost:      d.clientHost,
		Secret:          secret,
		CreatedBy:       d.approvedBy,
		ApprovedSession: d.approvedSession,
		ApprovedHost:    d.approvedHost,
	})
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	for _, rid := range revokedIDs {
		h.broker.DisconnectToken(rid)
	}
	writeJSON(w, http.StatusCreated, model.IssuedToken{Token: tok, Secret: secret})
}

func (h *handler) pgListTokens(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	filter := model.TokensActive
	if v := q.one("status"); v != "" {
		f, ok := model.ParseTokenFilter(v)
		if !ok {
			q.add("status", "active, revoked, all 중 하나여야 합니다.")
		}
		filter = f
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.ListTokens(r.Context(), filter)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) pgRevokeToken(w http.ResponseWriter, r *http.Request) {
	id, aerr := tokenID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	tok, err := h.pgStore.RevokeToken(r.Context(), TraceFrom(r.Context()), id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	h.broker.DisconnectToken(tok.ID)
	writeJSON(w, http.StatusOK, tok)
}

func (h *handler) pgRevokeCurrentToken(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred.shared {
		writeError(w, errSharedToken)
		return
	}
	tr := TraceFrom(r.Context())
	tr.Actor = cred.actor
	tok, err := h.pgStore.RevokeToken(r.Context(), tr, cred.tokenID)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	h.broker.DisconnectToken(tok.ID)
	writeJSON(w, http.StatusOK, tok)
}
