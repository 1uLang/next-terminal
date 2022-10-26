package repository

import (
	"context"

	"next-terminal/server/model"
)

type accessTokenRepository struct {
	baseRepository
}

func (repo accessTokenRepository) FindByUserId(ctx context.Context, userId string) (o model.AccessToken, err error) {
	err = repo.GetDB(ctx).Where("user_id = ?", userId).First(&o).Error
	return
}

func (repo accessTokenRepository) DeleteByUserId(ctx context.Context, userId string) error {
	return repo.GetDB(ctx).Where("user_id = ?", userId).Delete(&model.AccessToken{}).Error
}

func (repo accessTokenRepository) Create(ctx context.Context, o *model.AccessToken) error {
	return repo.GetDB(ctx).Create(o).Error
}

func (repo accessTokenRepository) FindAll(ctx context.Context) (o []model.AccessToken, err error) {
	err = repo.GetDB(ctx).Find(&o).Error
	return
}

func (r accessTokenRepository) List(c context.Context, pageIndex, pageSize int, ip, name string, ids []string, order, field string) (o []model.AccessGatewayForPage, total int64, err error) {
	t := model.AccessGateway{}
	db := r.GetDB(c).Table(t.TableName())
	dbCounter := r.GetDB(c).Table(t.TableName())

	if len(ip) > 0 {
		db = db.Where("ip like ?", "%"+ip+"%")
		dbCounter = dbCounter.Where("ip like ?", "%"+ip+"%")
	}
	if len(ids) > 0 {
		db = db.Where("id in ?", ids)
		dbCounter = dbCounter.Where("id in ?", ids)
	}

	if len(name) > 0 {
		db = db.Where("name like ?", "%"+name+"%")
		dbCounter = dbCounter.Where("name like ?", "%"+name+"%")
	}

	err = dbCounter.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	if order == "descend" {
		order = "desc"
	} else {
		order = "asc"
	}

	if field == "ip" {
		field = "ip"
	} else if field == "name" {
		field = "name"
	} else {
		field = "created"
	}

	err = db.Order(field + " " + order).Find(&o).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Error
	if o == nil {
		o = make([]model.AccessGatewayForPage, 0)
	}
	return
}
