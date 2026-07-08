package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/services"
)

type LogHandler struct {
	logSvc *services.LogService
}

func NewLogHandler(logSvc *services.LogService) *LogHandler {
	return &LogHandler{
		logSvc: logSvc,
	}
}

// ok and fail are usually defined in handler.go or similar, but we'll re-implement simple ones or assume they exist if they are exported.
// Since they might not be exported from api package to handlers package (usually they are lowercase fail()), we write local helpers.

func okLog(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": data,
	})
}

func failLog(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{
		"code": code,
		"msg":  msg,
	})
}

func (h *LogHandler) GetBackendLogs(c *gin.Context) {
	cursorStr := c.Query("cursor")
	cursor, _ := strconv.ParseInt(cursorStr, 10, 64)

	text, nextCursor, err := h.logSvc.ReadBackendLog(cursor)
	if err != nil {
		failLog(c, 500, "Failed to read backend logs: "+err.Error())
		return
	}

	okLog(c, gin.H{
		"text":        text,
		"next_cursor": nextCursor,
	})
}

func (h *LogHandler) ExportBackendLogs(c *gin.Context) {
	filePath := "./data/logs/backend.log"
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		failLog(c, 404, "Log file not found")
		return
	}
	c.FileAttachment(filePath, "backend.log")
}

func (h *LogHandler) ListClientLogs(c *gin.Context) {
	logs, err := h.logSvc.ListClientLogs()
	if err != nil {
		failLog(c, 500, "Failed to list client logs: "+err.Error())
		return
	}
	okLog(c, logs)
}

func (h *LogHandler) GetClientLog(c *gin.Context) {
	clientID := c.Param("id")
	cursorStr := c.Query("cursor")
	cursor, _ := strconv.ParseInt(cursorStr, 10, 64)

	text, nextCursor, err := h.logSvc.ReadClientLog(clientID, cursor)
	if err != nil {
		failLog(c, 500, "Failed to read client log: "+err.Error())
		return
	}

	okLog(c, gin.H{
		"text":        text,
		"next_cursor": nextCursor,
	})
}

func (h *LogHandler) ExportClientLog(c *gin.Context) {
	clientID := c.Param("id")
	filePath := filepath.Join("library/logs", clientID+".log")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		failLog(c, 404, "Log file not found")
		return
	}
	c.FileAttachment(filePath, clientID+".log")
}

func (h *LogHandler) DeleteClientLog(c *gin.Context) {
	clientID := c.Param("id")
	err := h.logSvc.DeleteClientLog(clientID)
	if err != nil {
		failLog(c, 500, "Failed to delete client log: "+err.Error())
		return
	}
	okLog(c, nil)
}
