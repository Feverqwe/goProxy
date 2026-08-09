package tray

type ReportPeriod int

const (
	ReportLastDay ReportPeriod = iota
	ReportLastSevenDays
	ReportAllTime
)
