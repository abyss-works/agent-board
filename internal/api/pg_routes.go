package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/ingest"
	"github.com/wsk0715/agent-board/internal/model"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

// maxPGDocumentBodyBytes caps the JSON envelope size of spec/record writes.
// It allows a full 2 MiB UTF-8 payload plus JSON escaping and metadata headroom
// without allowing oversized metadata payloads to bypass the content cap.
const maxPGDocumentBodyBytes = 3 * 1024 * 1024

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

	// Context retrieval, ready queue, and graph edge review
	route(mux, "/v1/context/query", methods{"POST": h.pgContextQuery})
	route(mux, "/v1/projects/{key}/ready", methods{"GET": h.pgReadyIssues})
	route(mux, "/v1/edges", methods{"POST": h.pgCreateEdge})
	route(mux, "/v1/edges/candidates", methods{"GET": h.pgListEdgeCandidates})
	route(mux, "/v1/edges/{id}", methods{"DELETE": h.pgRemoveEdge})
	route(mux, "/v1/edges/{id}/confirm", methods{"POST": h.pgConfirmEdge})
	route(mux, "/v1/edges/{id}/reject", methods{"POST": h.pgRejectEdge})

	h.registerPGCompatRoutes(mux)

	// Sessions, messages, events, devices, tokens
	route(mux, "/v1/sessions", methods{"GET": h.pgListSessions, "POST": h.pgStartSession})
	route(mux, "/v1/sessions/{actor}/{session}", methods{"GET": h.pgGetSession, "PATCH": h.pgPatchSession})
	route(mux, "/v1/projects/{key}/messages", methods{"GET": h.pgListMessages, "POST": h.pgCreateMessage})
	route(mux, "/v1/messages/{id}", methods{"GET": h.pgGetMessage, "DELETE": h.pgDeleteMessage})
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
	case errors.Is(err, graph.ErrCycle):
		return conflict("cycle", "간선 관계가 순환합니다.")
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
	case errors.Is(err, pgstore.ErrSessionExists):
		return conflict("exists", "같은 행위자의 같은 세션 ID가 이미 있습니다.")
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
// invalid UTF-8 with U+FFFD), and enforces maxPGDocumentBodyBytes.
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
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPGDocumentBodyBytes))
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
	sorts := q.sorts("key", "created_at", "updated_at")
	limit, offset := q.limit(50, 200), q.offset()
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
	cs := make([]pgstore.CompatSort, len(sorts))
	for i, so := range sorts {
		cs[i] = pgstore.CompatSort{Field: so.Field, Desc: so.Desc}
	}
	items, total, err := h.pgStore.CompatListProjects(r.Context(), statuses, cs, limit, offset)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
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
	items, total, err := h.pgStore.ListEpics(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
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
		// The classic "open" means not closed: planned and active.
		statuses, err = model.ExpandMilestoneStageStatuses(strings.ReplaceAll(param, "open", "planned,active"))
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
	b, aerr := readBody(w, r, map[string]bool{"name": true, "description": true, "status": true, "target_date": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.CreateMilestoneInput
	if aerr := firstError(
		decodeField(b, "name", &in.Name),
		decodeField(b, "description", &in.Description),
		decodeField(b, "status", &in.Status),
		milestoneTargetDateRemoved(b),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	if in.Status == "open" {
		in.Status = model.MilestonePlanned
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
	b, aerr := readBody(w, r, map[string]bool{"name": true, "description": true, "status": true, "target_date": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := milestoneTargetDateRemoved(b); aerr != nil {
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
		if status == "open" {
			// The classic "open" means not closed: keep an active milestone active.
			status = model.MilestonePlanned
			if cur, err := h.pgStore.GetMilestone(r.Context(), id); err == nil && cur.Status == model.MilestoneActive {
				status = model.MilestoneActive
			}
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

var pgIssuePatchFields = withFields(pgIssueCreateFields, "status_reason", "depends_on", "parent", "topic")

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
	items, total, err := h.pgStore.ListIssues(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
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
	var nulls []Detail
	nonNull := func(name string, present, null bool, set func()) {
		if !present {
			return
		}
		if null {
			nulls = append(nulls, Detail{Field: name, Reason: "invalid_value", Message: "null일 수 없습니다."})
			return
		}
		set()
	}

	var title, bodyText, statusReason, recheck, decision, verNote, forceReason string
	var typ model.IssueType
	var status model.IssueStatus
	var prio model.Priority
	var verState model.VerificationState
	var labels, refs []string
	var epicID, milestoneID int64
	var force bool

	steps := []func() *apiError{
		func() *apiError {
			p, n, e := field(b, "title", &title)
			nonNull("title", p, n, func() { patch.Title = &title })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "body", &bodyText)
			nonNull("body", p, n, func() { patch.Body = &bodyText })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "type", &typ)
			nonNull("type", p, n, func() { patch.Type = &typ })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "status", &status)
			nonNull("status", p, n, func() { patch.Status = &status })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "priority", &prio)
			nonNull("priority", p, n, func() { patch.Priority = &prio })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "labels", &labels)
			nonNull("labels", p, n, func() { patch.Labels = &labels })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "refs", &refs)
			nonNull("refs", p, n, func() { patch.Refs = &refs })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "status_reason", &statusReason)
			nonNull("status_reason", p, n, func() { patch.StatusReason = &statusReason })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "verification_state", &verState)
			nonNull("verification_state", p, n, func() { patch.VerificationState = &verState })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "force", &force)
			nonNull("force", p, n, func() { patch.Force = force })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "reason", &forceReason)
			nonNull("reason", p, n, func() { patch.Reason = forceReason })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "epic_id", &epicID)
			if p {
				patch.EpicID.Set = true
				if !n {
					v := epicID
					patch.EpicID.Value = &v
				}
			}
			return e
		},
		func() *apiError {
			for _, key := range []string{"milestone_id", "milestone"} {
				p, n, e := field(b, key, &milestoneID)
				if e != nil {
					return e
				}
				if p {
					patch.MilestoneID.Set = true
					if !n {
						v := milestoneID
						patch.MilestoneID.Value = &v
					}
					break
				}
			}
			return nil
		},
		func() *apiError {
			p, n, e := field(b, "recheck_condition", &recheck)
			if p {
				patch.RecheckCondition.Set = true
				if !n {
					v := recheck
					patch.RecheckCondition.Value = &v
				}
			}
			return e
		},
		func() *apiError {
			p, n, e := field(b, "decision_needed", &decision)
			if p {
				patch.DecisionNeeded.Set = true
				if !n {
					v := decision
					patch.DecisionNeeded.Value = &v
				}
			}
			return e
		},
		func() *apiError {
			p, n, e := field(b, "verification_note", &verNote)
			if p {
				patch.VerificationNote.Set = true
				if !n {
					v := verNote
					patch.VerificationNote.Value = &v
				}
			}
			return e
		},
	}
	for _, step := range steps {
		if aerr := step(); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	if len(nulls) > 0 {
		writeError(w, validationFailed(nulls))
		return
	}

	var deps []string
	depsPresent := false
	if p, n, e := field(b, "depends_on", &deps); e != nil {
		writeError(w, e)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "depends_on", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		depsPresent = true
	}
	var classic []Detail
	if p, _, _ := field(b, "topic", new(json.RawMessage)); p {
		if raw := b["topic"]; !isNull(raw) {
			classic = append(classic, Detail{Field: "topic", Reason: "topic_removed", Message: "주제는 라벨로 대체되었습니다. labels를 쓰세요."})
		}
	}
	if len(classic) > 0 {
		writeError(w, validationFailed(classic))
		return
	}
	if p, _, _ := field(b, "parent", new(json.RawMessage)); p {
		patch.EpicID.Set = true
		if raw := b["parent"]; !isNull(raw) {
			var parentKey string
			if e := decodeField(b, "parent", &parentKey); e != nil {
				writeError(w, e)
				return
			}
			issueKey := r.PathValue("key")
			projectKey, _, _ := model.ParseIssueKey(issueKey)
			epicID, err := h.epicOfParent(r, projectKey, issueKey, parentKey)
			if err != nil {
				writeError(w, h.pgStoreError(r, err))
				return
			}
			patch.EpicID.Value = epicID
		}
	}

	var plan *dependsPlan
	if depsPresent {
		var perr error
		if plan, perr = h.planDependsOn(r, r.PathValue("key"), deps); perr != nil {
			writeError(w, h.pgStoreError(r, perr))
			return
		}
	}
	res, err := h.pgStore.UpdateIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), version, patch)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	issue := res.Issue
	if plan != nil {
		if err := h.applyDependsOn(r, issue.Key, plan); err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		if issue, err = h.pgStore.GetIssue(r.Context(), issue.Key); err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
	}
	w.Header().Set("ETag", etag(issue.Version))
	writeJSON(w, http.StatusOK, issue)
}

func (h *handler) pgDeleteIssue(w http.ResponseWriter, r *http.Request) {
	version, aerr := ifMatch(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	q := newQuery(r)
	reason := q.one("reason")
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	it, err := h.pgStore.DeleteIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), version, reason)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusOK, it)
}

func (h *handler) pgVerifyIssue(w http.ResponseWriter, r *http.Request) {
	h.pgSetVerification(w, r, true)
}

func (h *handler) pgUnverifyIssue(w http.ResponseWriter, r *http.Request) {
	h.pgSetVerification(w, r, false)
}

func (h *handler) pgSetVerification(w http.ResponseWriter, r *http.Request, verify bool) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	version, aerr := ifMatch(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var (
		it  pgstore.IssueDetail
		err error
	)
	if verify {
		it, err = h.pgStore.VerifyIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), version)
	} else {
		it, err = h.pgStore.UnverifyIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), version)
	}
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("ETag", etag(it.Version))
	writeJSON(w, http.StatusOK, it)
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
	waitParam := parseBool(q, "wait")
	waitTimeoutMS := q.intRange("wait_timeout_ms", 15000, 100, 30000)
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

	ingestStatus := string(res.IngestStatus)
	supersedesCandidates := []pgstore.SupersedesCandidate{}
	if waitParam != nil && *waitParam && res.IngestJobID > 0 {
		wRes, wErr := h.waitIngestJob(r.Context(), res.IngestJobID, time.Duration(waitTimeoutMS)*time.Millisecond)
		if wErr != nil {
			if r.Context().Err() != nil {
				return
			}
			h.opts.Logger.Warn("spec ingest wait failed", "job_id", res.IngestJobID, "err", wErr)
		} else {
			if wRes.TimedOut {
				ingestStatus = "timeout"
			} else {
				ingestStatus = string(wRes.Status)
			}
			if wRes.SupersedesCandidates != nil {
				supersedesCandidates = wRes.SupersedesCandidates
			}
		}
	}

	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
		w.Header().Set("Location", "/v1/projects/"+res.Spec.Project+"/specs/"+res.Spec.Slug)
	}
	writeJSON(w, status, map[string]any{
		"id":                    res.Spec.ID,
		"project":               res.Spec.Project,
		"slug":                  res.Spec.Slug,
		"kind":                  res.Spec.Kind,
		"title":                 res.Spec.Title,
		"tags":                  res.Spec.Tags,
		"current_revision_no":   res.Spec.CurrentRevisionNo,
		"sha256":                res.Spec.SHA256,
		"size_bytes":            res.Spec.SizeBytes,
		"archived":              res.Spec.Archived,
		"ingest_job_id":         res.IngestJobID,
		"ingest_status":         ingestStatus,
		"supersedes_candidates": supersedesCandidates,
		"updated_at":            res.Spec.UpdatedAt,
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
	items, total, err := h.pgStore.ListSpecs(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
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
	waitParam := parseBool(q, "wait")
	waitTimeoutMS := q.intRange("wait_timeout_ms", 15000, 100, 30000)
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

	ingestStatus := string(res.IngestStatus)
	supersedesCandidates := res.SupersedesCandidates
	if supersedesCandidates == nil {
		supersedesCandidates = []pgstore.SupersedesCandidate{}
	}
	if waitParam != nil && *waitParam && res.IngestJobID > 0 {
		wRes, wErr := h.waitIngestJob(r.Context(), res.IngestJobID, time.Duration(waitTimeoutMS)*time.Millisecond)
		if wErr != nil {
			if r.Context().Err() != nil {
				return
			}
			h.opts.Logger.Warn("record ingest wait failed", "job_id", res.IngestJobID, "err", wErr)
		} else {
			if wRes.TimedOut {
				ingestStatus = "timeout"
			} else {
				ingestStatus = string(wRes.Status)
			}
			if wRes.SupersedesCandidates != nil {
				supersedesCandidates = wRes.SupersedesCandidates
			}
		}
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
		"ingest_status":         ingestStatus,
		"supersedes_candidates": supersedesCandidates,
		"created_at":            res.Record.CreatedAt,
	})
}

func (h *handler) waitIngestJob(ctx context.Context, jobID int64, timeout time.Duration) (ingest.WaitResult, error) {
	if h.ingestQueue != nil {
		return h.ingestQueue.Wait(ctx, jobID, timeout)
	}
	q := ingest.NewQueue(h.pgStore.Pool(), ingest.QueueConfig{})
	defer q.Close()
	return q.Wait(ctx, jobID, timeout)
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
	items, total, err := h.pgStore.ListRecords(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
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
	f := pgstore.SessionFilter{Project: q.one("project"), Actor: q.one("actor")}
	if f.Project != "" && !model.ValidProjectKey(f.Project) {
		q.add("project", "프로젝트 키 형식이 아닙니다.")
	}
	if f.Actor != "" && !model.ValidActorID(f.Actor) {
		q.add("actor", "행위자 ID 형식이 아닙니다.")
	}
	if v := q.one("parent"); v != "" {
		if ref, ok := parseSessionRef(v); ok {
			f.Parent = &ref
		} else {
			q.add("parent", "<행위자>/<세션 ID> 형식이어야 합니다.")
		}
	}
	f.Active, f.Named, f.Waiting = parseBool(q, "active"), parseBool(q, "named"), parseBool(q, "waiting")
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.ListSessions(r.Context(), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) pgGetSession(w http.ResponseWriter, r *http.Request) {
	ref, aerr := sessionPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
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

func (h *handler) pgStartSession(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"session_id": true, "role": true, "issue": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in pgstore.ChildSessionInput
	if aerr := firstError(
		decodeField(b, "session_id", &in.SessionID),
		decodeOptional(b, "role", &in.Role),
		decodeOptional(b, "issue", &in.Issue),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	tr := TraceFrom(r.Context())
	if in.SessionID == "" {
		suffix, err := randomChars(h.opts.Rand, childSuffixChars, childSuffixLength)
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		in.SessionID = tr.SessionID + "." + suffix
		if len(in.SessionID) > maxSessionIDLen {
			writeError(w, validationFailed([]Detail{{
				Field:   "session_id",
				Reason:  "too_long",
				Message: fmt.Sprintf("부모 세션 ID로 만든 세션 ID가 %d자를 넘습니다. session_id를 직접 주세요.", maxSessionIDLen),
			}}))
			return
		}
	}
	s, err := h.pgStore.CreateChildSession(r.Context(), tr, in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/sessions/"+url.PathEscape(s.Actor)+"/"+url.PathEscape(s.SessionID))
	writeJSON(w, http.StatusCreated, s)
}

func (h *handler) pgPatchSession(w http.ResponseWriter, r *http.Request) {
	ref, aerr := sessionPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	tr := TraceFrom(r.Context())
	if a, ok := h.actor(tr.Actor); tr.Actor != ref.Actor && (!ok || a.Kind != model.ActorUser) {
		writeError(w, errNotSessionActor)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"parent": true, "role": true, "issue": true, "location": true, "state": true, "state_reason": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch pgstore.SessionPatch
	var details []Detail
	var parent, location map[string]json.RawMessage
	var role, issue, state, stateReason string
	steps := []func() *apiError{
		func() *apiError {
			present, null, e := field(b, "parent", &parent)
			if e != nil || !present {
				return e
			}
			patch.Parent.Set = true
			if !null {
				p, ok := sessionRefObject(parent)
				if !ok {
					details = append(details, Detail{Field: "parent", Reason: "invalid", Message: `{"actor","session_id"} 객체여야 합니다.`})
				}
				patch.Parent.Value = &p
			}
			return nil
		},
		func() *apiError {
			present, null, e := field(b, "role", &role)
			patch.Role.Set = present
			if present && !null {
				patch.Role.Value = &role
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "issue", &issue)
			patch.Issue.Set = present
			if present && !null {
				patch.Issue.Value = &issue
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "location", &location)
			if e != nil || !present {
				return e
			}
			patch.Location.Set = true
			if !null {
				loc, problems := parseSessionLocation(location)
				details = append(details, problems...)
				patch.Location.Value = &loc
			}
			return nil
		},
		func() *apiError {
			present, null, e := field(b, "state", &state)
			patch.State.Set = present
			if present && !null {
				patch.State.Value = &state
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "state_reason", &stateReason)
			patch.StateReason.Set = present
			if present && !null {
				patch.StateReason.Value = &stateReason
			}
			return e
		},
	}
	for _, step := range steps {
		if aerr := step(); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	if len(details) > 0 {
		writeError(w, validationFailed(details))
		return
	}
	s, err := h.pgStore.UpdateSession(r.Context(), tr, ref.Actor, ref.SessionID, patch)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	if ref.Actor == tr.Actor && ref.SessionID == tr.SessionID {
		keepWaiting(r)
		if !patch.State.Set && !patch.StateReason.Set {
			if at, cleared := h.clearWaiting(r.Context(), tr); cleared {
				s.State = nil
				s.StateReason = nil
				s.StateAt = nil
				s.UpdatedAt = at
			}
		}
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handler) pgListMessages(w http.ResponseWriter, r *http.Request) {
	projectKey, aerr := messageProjectKey(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	q := newQuery(r)
	tr := TraceFrom(r.Context())
	f := pgstore.MessageFilter{
		To:       q.one("to"),
		UnreadBy: q.one("unread_by"),
		Viewer:   &model.SessionRef{Actor: tr.Actor, SessionID: tr.SessionID},
	}
	if v := q.one("to_session"); v != "" {
		if ref, ok := parseSessionRef(v); ok {
			f.ToSession = &ref
		} else {
			q.add("to_session", "<행위자>/<세션 ID> 형식이어야 합니다.")
		}
	}
	if v := q.one("to_role"); v != "" {
		if _, ok := model.ParseSessionRole(v); !ok {
			q.add("to_role", "main, subagent, runner 중 하나여야 합니다.")
		} else {
			f.ToRole = v
		}
	}
	if v := q.one("issue"); v != "" {
		if !model.ValidIssueKey(v) {
			q.add("issue", "이슈 키 형식이어야 합니다.")
		} else {
			f.Issue = v
		}
	}
	if v := q.one("reply_to"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			q.add("reply_to", "1 이상의 정수여야 합니다.")
		} else {
			f.ReplyTo = &id
		}
	}
	if f.UnreadBy != "" && model.ValidateMessageReader(f.UnreadBy) != nil {
		q.add("unread_by", fmt.Sprintf("공백과 제어 문자 없이 1~%d자여야 합니다.", model.MaxMessageToRunes))
	}
	if all := parseBool(q, "all"); all != nil {
		f.All = *all
	}
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.ListMessages(r.Context(), projectKey, f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) pgCreateMessage(w http.ResponseWriter, r *http.Request) {
	projectKey, aerr := messageProjectKey(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{
		"to": true, "to_session": true, "to_role": true, "issue": true, "reply_to": true,
		"title": true, "body": true, "ttl": true,
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
	if raw, ok := b["to_session"]; ok && !isNull(raw) {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil && m != nil {
			if ref, ok := sessionRefObject(m); ok {
				in.ToSession = &ref
			}
		}
		if in.ToSession == nil {
			writeError(w, validationFailed([]Detail{{
				Field: "to_session", Reason: "invalid", Message: `{"actor","session_id"} 객체여야 합니다.`,
			}}))
			return
		}
	}
	m, err := h.pgStore.CreateMessage(r.Context(), TraceFrom(r.Context()), projectKey, in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/messages/%d", m.ID))
	writeJSON(w, http.StatusCreated, m)
}

func (h *handler) pgGetMessage(w http.ResponseWriter, r *http.Request) {
	id, aerr := messageID(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.pgStore.GetMessage(r.Context(), id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
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
	var afterVal int64
	hasAfter := false
	if afterRaw != "" {
		n, ok := parseNonNegativeInt64(afterRaw)
		if !ok {
			q.add("after", "0 이상의 정수여야 합니다.")
		} else {
			afterVal = n
			hasAfter = true
		}
	}
	project := q.one("project")
	if project != "" && !model.ValidProjectKey(project) {
		q.add("project", "프로젝트 키 형식이 아닙니다.")
	}
	kinds := q.csv("kind")

	lastEventIDRaw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	var cursor int64
	hasCursor := false
	if lastEventIDRaw != "" {
		n, ok := parseNonNegativeInt64(lastEventIDRaw)
		if !ok {
			q.details = append(q.details, Detail{Field: "Last-Event-ID", Reason: "invalid_header", Message: "0 이상의 정수여야 합니다."})
		} else {
			cursor = n
			hasCursor = true
		}
	} else if hasAfter {
		cursor = afterVal
		hasCursor = true
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}

	actor := TraceFrom(r.Context()).Actor
	cred := credentialFrom(r.Context())
	sub, unsub, err := h.broker.Subscribe(actor, cred.tokenID)
	if err != nil {
		switch {
		case errors.Is(err, errBrokerClosed):
			writeError(w, &apiError{Status: http.StatusServiceUnavailable, Code: "shutting_down", Message: "서버가 종료 중입니다."})
		case errors.Is(err, errTooManyByActor):
			writeError(w, tooManyRequests("행위자당 동시 이벤트 스트림 연결이 너무 많습니다."))
		case errors.Is(err, errTooManyStreams):
			writeError(w, tooManyRequests("동시 이벤트 스트림 연결이 너무 많습니다."))
		default:
			writeError(w, errInternal)
		}
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

	initialItems, initialNextAfter, err := h.pgStore.ListEvents(r.Context(), pgstore.EventFilter{
		After:   cursor,
		Project: project,
		Kinds:   kinds,
		Limit:   500,
	})
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}

	select {
	case <-sub.closed:
		if cred.tokenID > 0 {
			writeError(w, errUnauthorized)
		} else {
			writeError(w, &apiError{Status: http.StatusServiceUnavailable, Code: "shutting_down", Message: "서버가 종료 중입니다."})
		}
		return
	default:
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

	writeBatch := func(items []model.Event, nextAfter int64) error {
		if len(items) == 0 {
			return nil
		}
		for _, ev := range items {
			if err := writeSSEEvent(w, ev); err != nil {
				return err
			}
			cursor = ev.ID
		}
		if err := rc.Flush(); err != nil {
			return err
		}
		if nextAfter > cursor {
			cursor = nextAfter
		}
		return nil
	}

	drain := func() error {
		for {
			select {
			case <-sub.closed:
				return context.Canceled
			default:
			}
			items, nextAfter, err := h.pgStore.ListEvents(r.Context(), pgstore.EventFilter{
				After:   cursor,
				Project: project,
				Kinds:   kinds,
				Limit:   500,
			})
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return nil
			}
			if err := writeBatch(items, nextAfter); err != nil {
				return err
			}
			if len(items) < 500 {
				return nil
			}
		}
	}

	if err := writeBatch(initialItems, initialNextAfter); err != nil {
		return
	}
	if len(initialItems) == 500 {
		if err := drain(); err != nil {
			return
		}
	}

	pingEvery := h.opts.SSEPingInterval
	if pingEvery <= 0 {
		pingEvery = defaultSSEPingInterval
	}
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.broker.Done():
			return
		case <-sub.closed:
			return
		case <-sub.notify:
			select {
			case <-sub.closed:
				return
			default:
			}
			if err := drain(); err != nil {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
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

// --- Context Retrieval, Ready Queue, and Graph Edges ---

var pgContextQueryFields = map[string]bool{
	"query":              true,
	"project":            true,
	"include_global":     true,
	"source_types":       true,
	"max_results":        true,
	"limit":              true,
	"confidence_cutoff":  true,
	"expand_hops":        true,
	"include_archived":   true,
	"include_superseded": true,
}

func (h *handler) pgContextQuery(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, pgContextQueryFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var (
		req      pgstore.ContextQueryRequest
		limitVal int
		hasMax   bool
		hasLimit bool
	)
	if aerr := firstError(
		decodeField(b, "query", &req.Query),
		decodeField(b, "project", &req.Project),
		decodeOptional(b, "include_global", &req.IncludeGlobal),
		decodeList(b, "source_types", &req.SourceTypes),
		decodeOptional(b, "confidence_cutoff", &req.ConfidenceCutoff),
		decodeField(b, "expand_hops", &req.ExpandHops),
		decodeField(b, "include_archived", &req.IncludeArchived),
		decodeField(b, "include_superseded", &req.IncludeSuperseded),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	if p, n, err := field(b, "max_results", &req.MaxResults); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "max_results", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		hasMax = true
	}
	if p, n, err := field(b, "limit", &limitVal); err != nil {
		writeError(w, err)
		return
	} else if p {
		if n {
			writeError(w, validationFailed([]Detail{{Field: "limit", Reason: "invalid_value", Message: "null일 수 없습니다."}}))
			return
		}
		hasLimit = true
	}
	if !hasMax && hasLimit {
		req.MaxResults = limitVal
	}

	res, err := h.contextEngine.Query(r.Context(), req)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) pgReadyIssues(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.ReadyFilter{
		EpicKey: q.one("epic"),
		Label:   q.one("label"),
		Limit:   q.limit(20, 100),
		Offset:  q.offset(),
	}
	if v := q.one("milestone_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			q.add("milestone_id", "1 이상의 정수여야 합니다.")
		} else {
			f.MilestoneID = &id
		}
	}
	if inc := parseBool(q, "include_claimed"); inc != nil {
		f.IncludeClaimed = *inc
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}

	projKey, items, total, err := h.graphStore.ReadyIssuesByProjectKey(r.Context(), r.PathValue("key"), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": projKey,
		"items":   items,
		"total":   total,
		"limit":   f.Limit,
		"offset":  f.Offset,
	})
}

func (h *handler) pgListEdgeCandidates(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.CandidateFilter{
		Project: q.one("project"),
		Limit:   q.limit(50, 200),
		Offset:  q.offset(),
	}
	if f.Project != "" && !model.ValidProjectKey(f.Project) {
		q.add("project", "프로젝트 키 형식이 아닙니다.")
	}
	if v := q.one("kind"); v != "" {
		k, ok := model.ParseEdgeKind(v)
		if !ok {
			q.add("kind", "supersedes, about, depends_on, related 중 하나여야 합니다.")
		} else {
			f.Kind = k
		}
	}
	if v := q.one("source_type"); v != "" {
		st, ok := model.ParseGraphNodeType(v)
		if !ok {
			q.add("source_type", "issue, spec, record, epic 중 하나여야 합니다.")
		} else {
			f.SourceType = st
		}
	}
	if v := q.one("source_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			q.add("source_id", "1 이상의 정수여야 합니다.")
		} else {
			f.SourceID = id
		}
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}

	items, total, err := h.graphStore.ListCandidatesPaged(r.Context(), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

var pgCreateEdgeFields = map[string]bool{
	"project":                true,
	"source_type":            true,
	"source_id":              true,
	"target_type":            true,
	"target_id":              true,
	"unresolved_target_text": true,
	"kind":                   true,
	"confidence":             true,
	"evidence":               true,
}

func (h *handler) pgCreateEdge(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, pgCreateEdgeFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var (
		projectKey     string
		sourceTypeRaw  string
		sourceID       int64
		targetTypeRaw  *string
		targetID       *int64
		unresolvedText *string
		kindRaw        string
		confidence     *float64
		evidence       *string
	)
	if aerr := firstError(
		decodeField(b, "project", &projectKey),
		decodeField(b, "source_type", &sourceTypeRaw),
		decodeField(b, "source_id", &sourceID),
		decodeOptional(b, "target_type", &targetTypeRaw),
		decodeOptional(b, "target_id", &targetID),
		decodeOptional(b, "unresolved_target_text", &unresolvedText),
		decodeField(b, "kind", &kindRaw),
		decodeOptional(b, "confidence", &confidence),
		decodeOptional(b, "evidence", &evidence),
	); aerr != nil {
		writeError(w, aerr)
		return
	}

	var details []Detail
	srcType, ok := model.ParseGraphNodeType(sourceTypeRaw)
	if !ok {
		details = append(details, Detail{Field: "source_type", Reason: "invalid_value", Message: "issue, spec, record, epic 중 하나여야 합니다."})
	}
	if sourceID < 1 {
		details = append(details, Detail{Field: "source_id", Reason: "invalid_value", Message: "1 이상의 정수여야 합니다."})
	}
	kind, ok := model.ParseEdgeKind(kindRaw)
	if !ok {
		details = append(details, Detail{Field: "kind", Reason: "invalid_value", Message: "supersedes, about, depends_on, related 중 하나여야 합니다."})
	}

	var targetRef *pgstore.NodeRef
	hasTargetPair := targetTypeRaw != nil || targetID != nil
	hasUnresolved := unresolvedText != nil && strings.TrimSpace(*unresolvedText) != ""
	if hasTargetPair == hasUnresolved {
		details = append(details, Detail{Field: "target", Reason: "invalid_value", Message: "target_type/target_id 또는 unresolved_target_text 중 하나만 지정해야 합니다."})
	} else if hasTargetPair {
		if targetTypeRaw == nil || targetID == nil {
			details = append(details, Detail{Field: "target", Reason: "invalid_value", Message: "target_type과 target_id를 함께 지정해야 합니다."})
		} else {
			tt, ok := model.ParseGraphNodeType(*targetTypeRaw)
			if !ok {
				details = append(details, Detail{Field: "target_type", Reason: "invalid_value", Message: "issue, spec, record, epic 중 하나여야 합니다."})
			}
			if *targetID < 1 {
				details = append(details, Detail{Field: "target_id", Reason: "invalid_value", Message: "1 이상의 정수여야 합니다."})
			}
			if ok && *targetID >= 1 {
				targetRef = &pgstore.NodeRef{Type: tt, ID: *targetID}
			}
		}
	}
	if confidence != nil && (*confidence < 0 || *confidence > 1) {
		details = append(details, Detail{Field: "confidence", Reason: "invalid_value", Message: "0.0~1.0 사이여야 합니다."})
	}
	if len(details) > 0 {
		writeError(w, validationFailed(details))
		return
	}

	var projectID int64
	if strings.TrimSpace(projectKey) != "" {
		proj, err := h.pgStore.GetProject(r.Context(), projectKey)
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		projectID = proj.ID
	} else {
		pid, err := h.graphStore.ResolveNodeProjectID(r.Context(), pgstore.NodeRef{Type: srcType, ID: sourceID})
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		projectID = pid
	}

	edge, err := h.graphStore.UpsertExplicitEdge(r.Context(), pgstore.UpsertEdgeInput{
		ProjectID:            projectID,
		Source:               pgstore.NodeRef{Type: srcType, ID: sourceID},
		Target:               targetRef,
		UnresolvedTargetText: unresolvedText,
		Kind:                 kind,
		Confidence:           confidence,
		Evidence:             evidence,
	})
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/edges/%d", edge.ID))
	writeJSON(w, http.StatusCreated, edge)
}

func edgeIDPath(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, badRequest("간선 ID는 1 이상의 정수여야 합니다.", Detail{Field: "id", Reason: "invalid_path"})
	}
	return id, nil
}

func (h *handler) pgRemoveEdge(w http.ResponseWriter, r *http.Request) {
	id, aerr := edgeIDPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	edge, err := h.graphStore.RemoveEdge(r.Context(), TraceFrom(r.Context()).Actor, id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, edge)
}

func (h *handler) pgConfirmEdge(w http.ResponseWriter, r *http.Request) {
	id, aerr := edgeIDPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	edge, err := h.graphStore.ConfirmEdge(r.Context(), TraceFrom(r.Context()).Actor, id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, edge)
}

func (h *handler) pgRejectEdge(w http.ResponseWriter, r *http.Request) {
	id, aerr := edgeIDPath(r)
	if aerr == nil {
		aerr = newQuery(r).finish()
	}
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	edge, err := h.graphStore.RejectEdge(r.Context(), TraceFrom(r.Context()).Actor, id)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, edge)
}

// milestoneTargetDateRemoved accepts a null target_date, which the classic clients
// send when they set none, and refuses a date: milestones no longer carry one.
func milestoneTargetDateRemoved(b body) *apiError {
	raw, ok := b["target_date"]
	if !ok || isNull(raw) {
		return nil
	}
	return validationFailed([]Detail{{Field: "target_date", Reason: "target_date_removed", Message: "마일스톤의 목표일은 설계에서 제거되었습니다. 설명(description)에 적으세요."}})
}
