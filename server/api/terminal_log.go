package api

import (
	"context"
	"github.com/labstack/echo/v4"
	"next-terminal/server/common/maps"
	"next-terminal/server/repository"
	"strconv"
)

type TerminalLogApi struct{}

/*
*
查询终端命令日志
*/
func (terminalLogApi TerminalLogApi) TerminalLogPagingEndpoint(c echo.Context) error {
	pageIndex, _ := strconv.Atoi(c.QueryParam("pageIndex"))
	pageSize, _ := strconv.Atoi(c.QueryParam("pageSize"))
	assetId := c.QueryParam("assetId")
	message := c.QueryParam("message")
	startDate := c.QueryParam("startDate")
	endDate := c.QueryParam("endDate")
	items, total, err := repository.TerminalLogRepository.Find(context.Background(), pageIndex, pageSize, assetId, message, startDate, endDate)
	if err != nil {
		return err
	}
	return Success(c, maps.Map{
		"total": total,
		"items": items,
	})
}
