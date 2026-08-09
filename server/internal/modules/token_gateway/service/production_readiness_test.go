package service

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newProductionReadinessTestService(t *testing.T, rows *sqlmock.Rows) (*ProductionReadinessService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("(?s)LEFT JOIN ai_model_route_runtime_states.*last_health_check_at.*circuit_open_until.*p.exchange_rate<>1.*SELECT COUNT\\(\\*\\) FROM ai_price_skus.*sku.cost_unit_price/\\(1-p.min_margin_rate\\).*JSON_TABLE").WillReturnRows(rows)
	return NewProductionReadinessService(db), mock
}

func productionReadinessRows(values ...int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"published_models", "healthy_channels", "invalid_models", "missing_prices", "invalid_prices", "missing_routes", "low_margin_models", "active_safety_policies"}).
		AddRow(values[0], values[1], values[2], values[3], values[4], values[5], values[6], values[7])
}

func TestProductionReadinessValidatePass(t *testing.T) {
	service, mock := newProductionReadinessTestService(t, productionReadinessRows(6, 2, 0, 0, 0, 0, 0, 1))
	if err := service.Validate(context.Background()); err != nil {
		t.Fatalf("完整生产发布事实应通过: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionReadinessValidateFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		values []int64
	}{
		{name: "模型不足", values: []int64{4, 2, 0, 0, 0, 0, 0, 1}},
		{name: "渠道不足", values: []int64{6, 1, 0, 0, 0, 0, 0, 1}},
		{name: "模型参数不完整", values: []int64{6, 2, 1, 0, 0, 0, 0, 1}},
		{name: "缺价格", values: []int64{6, 2, 0, 1, 0, 0, 0, 1}},
		{name: "价格语义不完整", values: []int64{6, 2, 0, 0, 1, 0, 0, 1}},
		{name: "缺路由", values: []int64{6, 2, 0, 0, 0, 1, 0, 1}},
		{name: "毛利过低", values: []int64{6, 2, 0, 0, 0, 0, 1, 1}},
		{name: "审核策略不唯一", values: []int64{6, 2, 0, 0, 0, 0, 0, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := newProductionReadinessTestService(t, productionReadinessRows(tc.values...))
			if err := service.Validate(context.Background()); err == nil {
				t.Fatal("发布事实不完整时必须失败关闭")
			}
		})
	}
}
