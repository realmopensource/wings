package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/realmopensource/wings/router/middleware"
	"github.com/realmopensource/wings/server"
)

// postServerFirewall receives a whitelist payload from the panel and applies
// the corresponding iptables rules for the given allocation port.
func postServerFirewall(c *gin.Context) {
	s := middleware.ExtractServer(c)

	var data server.FirewallPayload
	if err := c.BindJSON(&data); err != nil {
		return
	}

	if data.Port < 1 || data.Port > 65535 {
		c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
			"error": "port must be between 1 and 65535",
		})
		return
	}

	if err := s.SyncFirewall(data); err != nil {
		middleware.CaptureAndAbort(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
