// Package firmware implements the /api/firmware endpoints by delegating to
// the engine's FirmwareService.
package firmware

import (
	"log/slog"
	"net/http"
	"sort"

	enginev1 "github.com/bzy-logics/ratavyr/gen/motioncore/engine/v1"
	"github.com/bzy-logics/ratavyr/internal/api/rest"
	"github.com/bzy-logics/ratavyr/internal/audit"
	"github.com/bzy-logics/ratavyr/internal/auth"
	"github.com/bzy-logics/ratavyr/internal/engine"
)

// Minimum roles: building only compiles in-repo board code; flashing
// replaces the firmware on a machine and is admin only.
const (
	BuildRole = auth.Operator
	FlashRole = auth.Admin
)

// Handler serves firmware targets and build/flash jobs.
type Handler struct {
	Engine *engine.Client
	Log    *slog.Logger
}

// Register adds the routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/firmware/targets", h.targets)
	mux.HandleFunc("GET /api/firmware/jobs", h.jobs)
	mux.HandleFunc("GET /api/firmware/jobs/{id}", h.job)
	mux.HandleFunc("POST /api/firmware/builds", auth.Require(BuildRole, h.build))
	mux.HandleFunc("POST /api/firmware/flashes", auth.Require(FlashRole, h.flash))
}

func (h *Handler) targets(w http.ResponseWriter, r *http.Request) {
	resp, err := engine.Call(r.Context(), h.Engine, h.Engine.Firmware.ListTargets, &enginev1.ListTargetsRequest{})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "ListTargets", err)
		return
	}
	rest.WriteJSON(w, http.StatusOK, rest.FromFirmwareTargets(resp.GetTargets()))
}

func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	resp, err := engine.Call(r.Context(), h.Engine, h.Engine.Firmware.ListJobs, &enginev1.ListJobsRequest{})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "ListJobs", err)
		return
	}
	jobs := make([]rest.FirmwareJob, 0, len(resp.GetJobs()))
	for _, j := range resp.GetJobs() {
		jobs = append(jobs, rest.FromFirmwareJob(j))
	}
	// The REST API promises newest first; the proto does not specify an
	// order. Stable, so the engine's order is kept for equal timestamps.
	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].CreatedUnix > jobs[b].CreatedUnix })
	rest.WriteJSON(w, http.StatusOK, jobs)
}

func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	j, err := engine.Call(r.Context(), h.Engine, h.Engine.Firmware.GetJob, &enginev1.GetJobRequest{Id: r.PathValue("id")})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "GetJob", err)
		return
	}
	rest.WriteJSON(w, http.StatusOK, rest.FromFirmwareJob(j))
}

func (h *Handler) build(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MCU *string `json:"mcu"`
	}
	if !rest.DecodeObject(w, r, &body) {
		return
	}
	req := &enginev1.StartBuildRequest{}
	if body.MCU != nil {
		req.Mcu = *body.MCU
	}
	audit.Note(r.Context(), "mcu", req.Mcu)
	j, err := engine.Call(r.Context(), h.Engine, h.Engine.Firmware.StartBuild, req)
	if err != nil {
		rest.WriteEngineError(w, h.Log, "StartBuild", err)
		return
	}
	rest.WriteJSON(w, http.StatusAccepted, rest.FromFirmwareJob(j))
}

func (h *Handler) flash(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BuildJobID *string `json:"build_job_id"`
		Confirm    *bool   `json:"confirm"`
	}
	if !rest.DecodeObject(w, r, &body) {
		return
	}
	if body.BuildJobID == nil || *body.BuildJobID == "" {
		rest.BadRequest(w, `missing parameter "build_job_id"`)
		return
	}
	if body.Confirm == nil || !*body.Confirm {
		rest.BadRequest(w, `flashing requires "confirm": true (drives must be powered off)`)
		return
	}
	audit.Note(r.Context(), "build_job_id", *body.BuildJobID)
	j, err := engine.Call(r.Context(), h.Engine, h.Engine.Firmware.StartFlash,
		&enginev1.StartFlashRequest{BuildJobId: *body.BuildJobID, Confirm: true})
	if err != nil {
		rest.WriteEngineError(w, h.Log, "StartFlash", err)
		return
	}
	rest.WriteJSON(w, http.StatusAccepted, rest.FromFirmwareJob(j))
}
