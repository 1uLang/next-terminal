package repository

import (
	"context"

	"next-terminal/server/common/nt"
	"next-terminal/server/model"
)

var CredentialRepository = new(credentialRepository)

type credentialRepository struct {
	baseRepository
}

func (r credentialRepository) FindByAll(c context.Context) (o []model.CredentialSimpleVo, err error) {
	db := r.GetDB(c).Table("credentials")
	err = db.Find(&o).Error
	return
}

func (r credentialRepository) Find(c context.Context, pageIndex, pageSize int, name, order, field string) (o []model.CredentialForPage, total int64, err error) {
	db := r.GetDB(c).Table("credentials").Select("credentials.id,credentials.name,credentials.type,credentials.username,credentials.owner,credentials.created,users.nickname as owner_name").Joins("left join users on credentials.owner = users.id")
	dbCounter := r.GetDB(c).Table("credentials")

	if len(name) > 0 {
		db = db.Where("credentials.name like ?", "%"+name+"%")
		dbCounter = dbCounter.Where("credentials.name like ?", "%"+name+"%")
	}

	err = dbCounter.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	if order == "ascend" {
		order = "asc"
	} else {
		order = "desc"
	}

	if field == "name" {
		field = "name"
	} else {
		field = "created"
	}

	err = db.Order("credentials." + field + " " + order).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Find(&o).Error
	if o == nil {
		o = make([]model.CredentialForPage, 0)
	}
	return
}

func (r credentialRepository) Create(c context.Context, o *model.Credential) (err error) {
	return r.GetDB(c).Create(o).Error
}

func (r credentialRepository) FindById(c context.Context, id string) (o model.Credential, err error) {
	err = r.GetDB(c).Where("id = ?", id).First(&o).Error
	return
}

func (r credentialRepository) UpdateById(c context.Context, o *model.Credential, id string) error {
	o.ID = id
	return r.GetDB(c).Updates(o).Error
}

func (r credentialRepository) DeleteById(c context.Context, id string) error {
	return r.GetDB(c).Where("id = ?", id).Delete(&model.Credential{}).Error
}

func (r credentialRepository) Count(c context.Context) (total int64, err error) {
	err = r.GetDB(c).Find(&model.Credential{}).Count(&total).Error
	return
}

func (r credentialRepository) FindAll(c context.Context) (o []model.Credential, err error) {
	err = r.GetDB(c).Find(&o).Error
	return
}

func (r credentialRepository) List(c context.Context, pageIndex, pageSize int, name, order, field string, ids []string, account *model.User) (o []model.CredentialForPage, total int64, err error) {
	db := r.GetDB(c).Table("credentials").Select("credentials.id,credentials.name,credentials.type,credentials.username,credentials.owner,credentials.created,users.nickname as owner_name,COUNT(resource_sharers.user_id) as sharer_count").
		Joins("left join users on credentials.owner = users.id").Joins("left join resource_sharers on credentials.id = resource_sharers.resource_id").
		Group("credentials.id")
	dbCounter := r.GetDB(c).Table("credentials").Select("DISTINCT credentials.id").
		Joins("left join resource_sharers on credentials.id = resource_sharers.resource_id").
		Group("credentials.id")

	if nt.TypeUser == account.Type {
		owner := account.ID
		db = db.Where("credentials.owner = ? or resource_sharers.user_id = ?", owner, owner)
		dbCounter = dbCounter.Where("credentials.owner = ? or resource_sharers.user_id = ?", owner, owner)
	}
	if len(ids) > 0 {
		db = db.Where("credentials.id in ?", ids)
		dbCounter = dbCounter.Where("credentials.id in ?", ids)
	}
	if len(name) > 0 {
		db = db.Where("credentials.name like ?", "%"+name+"%")
		dbCounter = dbCounter.Where("credentials.name like ?", "%"+name+"%")
	}

	err = dbCounter.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	if order == "ascend" {
		order = "asc"
	} else {
		order = "desc"
	}

	if field == "name" {
		field = "name"
	} else {
		field = "created"
	}

	err = db.Order("credentials." + field + " " + order).Offset((pageIndex - 1) * pageSize).Limit(pageSize).Find(&o).Error
	if o == nil {
		o = make([]model.CredentialForPage, 0)
	}
	return
}
