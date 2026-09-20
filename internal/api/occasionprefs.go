package api

import (
	"github.com/gin-gonic/gin"

	"wiminder/internal/domain"
	"wiminder/internal/store"
)

// Per-occasion reminder prefs: the same shape as the contact-level prefs, plus
// the type suggestions the SPA offers when adding an occasion. All handlers are
// owner-scoped via s.scope(c); a malformed path id is a 404 (pathID).

// occasionPrefsIn: full-replace payload. An absent offsets map decodes as nil
// and is stored as {} = inherit every stream. An absent custom decodes as
// true: legacy payloads always meant custom-on. Unlike the contact-level PUT
// (handleSetPrefs), which merges omitted fields from the stored row, this one
// is full-replace: a flag-only payload resets every unspecified field
// (offsets → {}, channel_ids → [], enabled → true).
type occasionPrefsIn struct {
	Offsets    domain.OffsetMap `json:"offsets"`
	ChannelIDs *[]string        `json:"channel_ids"`
	Enabled    *bool            `json:"enabled"`
	Custom     *bool            `json:"custom"`
}

// handleGetOccasionPrefs: without a row the occasion inherits, which the SPA
// renders as the explicit default payload (enabled, no offsets) — not a 404.
func (s *Server) handleGetOccasionPrefs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	occ, err := s.st.OccasionByID(c.Request.Context(), s.scope(c), id)
	if err != nil {
		respondErr(c, err)
		return
	}
	if occ.Prefs == nil {
		c.JSON(200, store.OccasionPrefs{OccasionID: id, Offsets: domain.OffsetMap{}, ChannelIDs: []string{}, Enabled: true, Custom: false})
		return
	}
	c.JSON(200, *occ.Prefs)
}

func (s *Server) handleSetOccasionPrefs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	in, ok := bind[occasionPrefsIn](c)
	if !ok {
		return
	}
	if _, err := s.st.OccasionByID(c.Request.Context(), s.scope(c), id); err != nil {
		respondErr(c, err)
		return
	}
	if err := domain.ValidateOffsetMap(in.Offsets); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	p := store.OccasionPrefs{OccasionID: id, Offsets: in.Offsets, ChannelIDs: []string{}, Enabled: true, Custom: true}
	if p.Offsets == nil {
		p.Offsets = domain.OffsetMap{}
	}
	if in.ChannelIDs != nil {
		p.ChannelIDs = *in.ChannelIDs
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if in.Custom != nil {
		p.Custom = *in.Custom
	}
	if err := s.st.SetOccasionPrefs(c.Request.Context(), p); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, p)
}

// handleDeleteOccasionPrefs: back to inherit (the row is deleted, not zeroed).
func (s *Server) handleDeleteOccasionPrefs(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if _, err := s.st.OccasionByID(c.Request.Context(), s.scope(c), id); err != nil {
		respondErr(c, err)
		return
	}
	if err := s.st.DeleteOccasionPrefs(c.Request.Context(), id); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// handleOccasionTypes: suggestions for the occasion type input — the caller's
// own used types plus the built-ins.
func (s *Server) handleOccasionTypes(c *gin.Context) {
	types, err := s.st.DistinctOccasionTypes(c.Request.Context(), s.scope(c))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, gin.H{"types": types})
}
