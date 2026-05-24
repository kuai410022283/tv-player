package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/tvplayer/backend/internal/models"
	"github.com/tvplayer/backend/internal/services"
)

type PlanHandler struct {
	svc *services.PlanService
}

func NewPlanHandler(svc *services.PlanService) *PlanHandler {
	return &PlanHandler{svc: svc}
}

func (h *PlanHandler) GetPlans(c *gin.Context) {
	items, err := h.svc.GetPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success", Data: items})
}

func (h *PlanHandler) AddPlan(c *gin.Context) {
	var m models.SubscriptionPlan
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid request"})
		return
	}
	if m.Name == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "name is required"})
		return
	}

	if err := h.svc.AddPlan(&m); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success", Data: m})
}

func (h *PlanHandler) UpdatePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid id"})
		return
	}
	var m models.SubscriptionPlan
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid request"})
		return
	}
	m.ID = id
	if m.Name == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "name is required"})
		return
	}

	if err := h.svc.UpdatePlan(&m); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success", Data: m})
}

func (h *PlanHandler) DeletePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid id"})
		return
	}

	if err := h.svc.DeletePlan(id); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success"})
}
