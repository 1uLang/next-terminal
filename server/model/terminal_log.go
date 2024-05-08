package model

import "next-terminal/server/common"

type TerminalLog struct {
	ID        string          `gorm:"primary_key,type:varchar(50)" json:"id"`
	SessionId string          `gorm:"type:varchar(50)" json:"sessionId"`
	AssetId   string          `gorm:"type:varchar(50)" json:"assetId"`
	ClientIP  string          `gorm:"type:varchar(200)" json:"clientIp"`
	Message   string          `json:"message"`
	Created   common.JsonTime `json:"created"`
}

func (r *TerminalLog) TableName() string {
	return "terminal_log"
}
