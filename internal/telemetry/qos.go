package telemetry

import "encoding/json"

type QoSDevice struct {
	Interface string `json:"interface"`
	Kind      string `json:"kind"`
	Root      bool   `json:"root"`
	Parent    string `json:"parent,omitempty"`
}
type QoSStatus struct {
	Available bool        `json:"available"`
	Devices   []QoSDevice `json:"devices"`
}

const maxQoSOutputBytes = 256 << 10

func parseQoSStatus(data []byte) QoSStatus {
	status := QoSStatus{Devices: []QoSDevice{}}
	if len(data) > maxQoSOutputBytes {
		return status
	}
	var records []struct {
		Device string `json:"dev"`
		Kind   string `json:"kind"`
		Root   bool   `json:"root"`
		Parent string `json:"parent"`
	}
	if json.Unmarshal(data, &records) != nil || records == nil || len(records) > 1024 {
		return status
	}
	for _, record := range records {
		if record.Device == "" || len(record.Device) > 15 || record.Kind == "" || len(record.Kind) > 32 || len(record.Parent) > 32 {
			return QoSStatus{Devices: []QoSDevice{}}
		}
		status.Devices = append(status.Devices, QoSDevice{Interface: record.Device, Kind: record.Kind, Root: record.Root, Parent: record.Parent})
	}
	status.Available = true
	return status
}
