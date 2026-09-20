package api

import (
	"github.com/gin-gonic/gin"

	"wiminder/internal/notify"
)

// handleChannelTest sends a test message to the channel — end-to-end config validation.
func (s *Server) handleChannelTest(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ch, err := s.st.GetChannel(c.Request.Context(), s.scope(c), id)
	if err != nil {
		respondErr(c, err)
		return
	}
	n, err := notify.NewFromChannel(*ch, s.key)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := n.Test(c.Request.Context()); err != nil {
		c.JSON(502, gin.H{"error": "test send failed: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
