package cconf

// DhLogExportConfig 是 dh 二开「日志导出」的服务端配置。
//
// 独立成文件而不是塞进官方 conf.go，是为了压低 merge upstream 的冲突面
// （与 conf_dh_proxy.go 同一模式）。
type DhLogExportConfig struct {
	// MaxRows 单次导出条数上限，与 FE src/dh/logExport/constants.ts 的
	// MAX_ROWS_TIER2 对齐。改这个值需要重新编译，见
	// pharos-ops/HANDOFF-log-export.md 决策点 3。
	MaxRows int
}

var DhLogExport = DhLogExportConfig{
	MaxRows: 1000000, // 与 FE 的 MAX_ROWS_TIER2 对齐
}
