package model

import (
	"gorm.io/gorm"
)

type DBConf struct {
	ModelPkg  string
	TableName string
	ModelName string

	SchemaNameOpts []SchemaNameOpt
	MemberOpts     []MemberOpt

	FieldNullable     bool
	FieldWithIndexTag bool
}

func (cf *DBConf) SortOpt() (modifyOpts []MemberOpt, filterOpts []MemberOpt, createOpts []MemberOpt) {
	if cf == nil {
		return
	}
	return sortOpt(cf.MemberOpts)
}

func (cf *DBConf) GetSchemaName(db *gorm.DB) string {
	if cf == nil {
		return defaultMysqlSchemaNameOpt(db)
	}
	for _, opt := range cf.SchemaNameOpts {
		if name := opt(db); name != "" {
			return name
		}
	}
	return defaultMysqlSchemaNameOpt(db)
}
