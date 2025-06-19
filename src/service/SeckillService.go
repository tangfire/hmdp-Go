package service

import "hmdp-Go/src/model"

type SecKillService struct {
}

var SecKillManager *SecKillService

func (*SecKillService) QuerySeckillVoucherById(id int64) (model.SecKillVoucher, error) {
	var result model.SecKillVoucher
	return result, result.QuerySeckillVoucherById(id)
}
