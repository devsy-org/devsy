package analytics

// Reserved routing/identity keys.
const (
	KeyType      = "type"
	KeyMachineID = "machine_id"
	KeyTimestamp = "timestamp"
)

type Event map[string]map[string]any

type Client interface {
	RecordEvent(Event)
	Flush()
}
