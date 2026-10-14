package wa

import (
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	wmstore "go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

func historyProps(days, sizeMB int) *waCompanionReg.DeviceProps {
	p := proto.Clone(wmstore.DeviceProps).(*waCompanionReg.DeviceProps)
	p.RequireFullSync = proto.Bool(true)
	if p.HistorySyncConfig == nil {
		p.HistorySyncConfig = &waCompanionReg.DeviceProps_HistorySyncConfig{}
	}
	p.HistorySyncConfig.FullSyncDaysLimit = proto.Uint32(uint32(days))
	p.HistorySyncConfig.FullSyncSizeMbLimit = proto.Uint32(uint32(sizeMB))
	return p
}
