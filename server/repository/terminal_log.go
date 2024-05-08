package repository

import (
	"context"
	"next-terminal/server/model"
)

var TerminalLogRepository = new(terminalLogRepository)

type terminalLogRepository struct {
	baseRepository
}

func (r terminalLogRepository) FindAll(c context.Context) (o []model.TerminalLog, err error) {
	err = r.GetDB(c).Find(&o).Error
	return
}

func (r terminalLogRepository) Find(c context.Context, pageIndex, pageSize int, assetId, message, startDate, endDate string) (o []model.TerminalLog, total int64, err error) {
	m := model.TerminalLog{}
	db := r.GetDB(c).Table(m.TableName())
	dbCounter := r.GetDB(c).Table(m.TableName())

	if message != "" {
		db = db.Where("message like ?", "%"+message+"%")
		dbCounter = dbCounter.Where("message like ?", "%"+message+"%")
	}
	if assetId != "" {
		db = db.Where("assetId = ?", assetId)
		dbCounter = dbCounter.Where("assetId = ?", assetId)
	}
	if startDate != "" {
		db = db.Where("created >= ?", startDate)
		dbCounter = dbCounter.Where("created >= ?", startDate)
	}
	if endDate != "" {
		db = db.Where("created <= ?", endDate)
		dbCounter = dbCounter.Where("created <= ?", endDate)
	}

	err = dbCounter.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	err = db.Order("created desc").Find(&o).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Error
	if o == nil {
		o = make([]model.TerminalLog, 0)
	}
	return
}

func (r terminalLogRepository) Create(c context.Context, m *model.TerminalLog) error {
	return r.GetDB(c).Create(m).Error
}
