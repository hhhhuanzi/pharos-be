package router

import (
	"net/http"

	"github.com/ccfos/nightingale/v6/models"
	"github.com/ccfos/nightingale/v6/pkg/ginx"

	"github.com/gin-gonic/gin"
)

func (rt *Router) dhBoardFavoriteGets(c *gin.Context) {
	me := c.MustGet("user").(*models.User)
	ids, err := models.DhBoardFavoriteIdsByUser(rt.Ctx, me.Id)
	if ids == nil {
		ids = []int64{}
	}
	ginx.NewRender(c).Data(ids, err)
}

func (rt *Router) dhBoardFavoriteAdd(c *gin.Context) {
	me := c.MustGet("user").(*models.User)
	boardId := ginx.UrlParamInt64(c, "id")
	err := models.DhBoardFavoriteAdd(rt.Ctx, me.Id, boardId)
	if err == models.ErrBoardNotFound {
		ginx.NewRender(c, http.StatusNotFound).Message(err.Error())
		return
	}
	ginx.NewRender(c).Message(err)
}

func (rt *Router) dhBoardFavoriteDel(c *gin.Context) {
	me := c.MustGet("user").(*models.User)
	boardId := ginx.UrlParamInt64(c, "id")
	err := models.DhBoardFavoriteDel(rt.Ctx, me.Id, boardId)
	ginx.NewRender(c).Message(err)
}
