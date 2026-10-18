package server

import (
	"net/http"
	"strings"

	"github.com/LeonardoIz/artifax/skills"
)

type skillData struct {
	Markdown string
	BaseURL  string
}

// skillMarkdown returns the agent skill with this instance's URL filled in.
func (s *Server) skillMarkdown() string {
	return strings.ReplaceAll(skills.Artifax, skills.ExampleURL, s.cfg.URL(""))
}

// handleSkill serves the agent skill: a page for browsers, Markdown for agents and curl.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		s.handleSkillRaw(w, r)
		return
	}
	w.Header().Set("Vary", "Accept")
	s.render(w, http.StatusOK, "skill", s.page(s.currentSession(r), "Agent skill", skillData{
		Markdown: s.skillMarkdown(),
		BaseURL:  s.cfg.URL(""),
	}))
}

func (s *Server) handleSkillRaw(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/markdown; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Vary", "Accept")
	if r.URL.Query().Has("download") {
		h.Set("Content-Disposition", `attachment; filename="SKILL.md"`)
	}
	w.Write([]byte(s.skillMarkdown()))
}
