package model

import (
	"errors"
	"github.com/jinzhu/gorm"
	"hmdp-Go/src/config/mysql"
	"time"
)

const SECKILL_VOUCHER_NAME = "tb_seckill_voucher"

type SecKillVoucher struct {
	VoucherId  int64     `gorm:"primary;column:voucher_id" json:"voucherId"`
	Stock      int       `gorm:"column:stock" json:"stock"`
	CreateTime time.Time `gorm:"column:create_time" json:"createTime"`
	BeginTime  time.Time `gorm:"column:begin_time" json:"beginTime"`
	EndTime    time.Time `gorm:"column:end_time" json:"endTime"`
	UpdateTime time.Time `gorm:"column:update_time" json:"updateTime"`
}

func (*SecKillVoucher) TableName() string {
	return SECKILL_VOUCHER_NAME
}

func (sec *SecKillVoucher) AddSeckillVoucher(tx *gorm.DB) error {
	return tx.Table(sec.TableName()).Create(sec).Error
}

func (sec *SecKillVoucher) QuerySeckillVoucherById(id int64) error {
	return mysql.GetMysqlDB().Table(sec.TableName()).Where("voucher_id = ?", id).First(sec).Error
}

// 直接通过库存条件实现乐观锁
func (sv *SecKillVoucher) DecrVoucherStock(voucherId int64, tx *gorm.DB) error {
	result := tx.Model(sv).
		Where("voucher_id = ? AND stock > 0", voucherId).
		Update("stock", gorm.Expr("stock - 1"))

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 { // 核心判断
		return errors.New("库存不足")
	}
	return nil
}
