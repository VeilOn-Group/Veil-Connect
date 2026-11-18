package localapi

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/veilon/veil-shared/pkg/crash"
	"github.com/veilon/veilon-app/veilond/internal/apiclient"
	"github.com/veilon/veilon-app/veilond/internal/crashq"
)

// Crash reports (ADR-0077): the UI lists the queued reports with their
// full text (exactly what would be sent), sends or deletes them, and the
// desktop UI process submits its own previous crash.

func (s *Server) crashRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/crash-reports", s.crashList)
	mux.HandleFunc("POST /v1/crash-reports", s.crashSubmit)
	mux.HandleFunc("POST /v1/crash-reports/{id}/send", s.crashSend)
	mux.HandleFunc("POST /v1/crash-reports/{id}/delete", s.crashDelete)
}

type crashView struct {
	ID               string `json:"id"`
	Component        string `json:"component"`
	Kind             string `json:"kind"`
	Day              string `json:"day"`
	AppVersion       string `json:"app_version"`
	ComponentVersion string `json:"component_version,omitempty"`
	Title            string `json:"title"`
	Text             string `json:"text"`
}

func (s *Server) crashList(w http.ResponseWriter, _ *http.Request) {
	out := struct {
		AutoSend bool        `json:"auto_send"`
		Reports  []crashView `json:"reports"`
	}{AutoSend: s.m.Settings().CrashReports, Reports: []crashView{}}
	for _, r := range s.m.CrashReports() {
		out.Reports = append(out.Reports, crashView{
			ID: r.ID, Component: r.Component, Kind: r.Kind, Day: r.Day, AppVersion: r.AppVersion,
			ComponentVersion: r.ComponentVersion, Title: crash.Title(r.Text), Text: r.Text,
		})
	}
	writeJSON(w, 200, out)
}

var reUIVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,31}$`)

// crashSubmit queues a crash of the desktop UI process (its Go traceback
// from the previous run). Only component "desktop" can be submitted; the
// text is sanitized by the queue like any other report.
func (s *Server) crashSubmit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string `json:"kind"`
		Version string `json:"version"`
		Text    string `json:"text"`
	}
	if err := decode(r, &in); err != nil || in.Kind != crash.KindPanic || !reUIVersion.MatchString(in.Version) ||
		in.Text == "" || len(in.Text) > 4*crash.MaxText {
		writeErr(w, 400, "bad_request", "invalid crash report")
		return
	}
	tb, ok := crash.FromGoOutput(in.Text)
	if !ok {
		writeErr(w, 400, "bad_request", "no traceback")
		return
	}
	s.m.RecordCrash(crash.ComponentDesktop, crash.KindPanic, in.Version, tb)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) crashSend(w http.ResponseWriter, r *http.Request) {
	if err := s.m.SendCrashReport(r.Context(), r.PathValue("id")); err != nil {
		s.crashFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) crashDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DeleteCrashReport(r.PathValue("id")); err != nil {
		s.crashFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) crashFail(w http.ResponseWriter, err error) {
	var apiErr *apiclient.APIError
	switch {
	case errors.Is(err, crashq.ErrNotFound):
		writeErr(w, 404, "not_found", "no such report")
	case errors.As(err, &apiErr):
		writeErr(w, apiErr.Status, apiErr.Body.Code, apiErr.Body.Message)
	default:
		s.fail(w, err)
	}
}
