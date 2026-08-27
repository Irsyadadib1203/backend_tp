package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"topup-backend/internal/pkg/response"
	"topup-backend/internal/service"
)

type GameHandler struct {
	gameService     service.GameService
	nicknameService service.NicknameService
}

func NewGameHandler(gameService service.GameService, nicknameService service.NicknameService) *GameHandler {
	return &GameHandler{
		gameService:     gameService,
		nicknameService: nicknameService,
	}
}

func (h *GameHandler) GetGames(c *gin.Context) {
	games, err := h.gameService.GetPublicGames()
	if err != nil {
		response.InternalServerError(c, "Failed to retrieve games", err)
		return
	}

	response.Success(c, "Games retrieved successfully", games)
}

func (h *GameHandler) GetGameBySlug(c *gin.Context) {
	slug := c.Param("slug")
	game, err := h.gameService.GetGameBySlug(slug)
	if err != nil || game == nil {
		response.NotFound(c, "Game not found")
		return
	}

	response.Success(c, "Game details retrieved successfully", game)
}

func (h *GameHandler) CheckNickname(c *gin.Context) {
	var req struct {
		GameCode string `json:"game_code" binding:"required"`
		UserID   string `json:"user_id" binding:"required"`
		ServerID string `json:"server_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid input for nickname check", err.Error())
		return
	}

	result, err := h.nicknameService.CheckNickname(req.GameCode, req.UserID, req.ServerID)
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	response.Success(c, "Nickname validated successfully", result)
}

func (h *GameHandler) CheckUserFromQuery(c *gin.Context) {
	gameParam := c.Param("game")
	userID := c.Query("user_id")
	serverID := c.Query("zone_id")
	if serverID == "" {
		serverID = c.Query("server_id")
	}

	result, err := h.nicknameService.CheckNickname(gameParam, userID, serverID)
	if err != nil {
		response.BadRequest(c, err.Error(), nil)
		return
	}

	c.JSON(200, gin.H{
		"success":  true,
		"nickname": result.Nickname,
		"data":     result.Nickname,
	})
}

func (h *GameHandler) GetNominalsByGame(c *gin.Context) {
	gameIDStr := c.Param("id")
	gameID, _ := strconv.Atoi(gameIDStr)

	game, err := h.gameService.GetGameByID(uint(gameID))
	if err != nil || game == nil {
		response.NotFound(c, "Game not found")
		return
	}

	response.Success(c, "Nominals retrieved successfully", game.Nominals)
}
