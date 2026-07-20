package analytics

// Reserved payload keys that carry event routing/identity rather than
// analytics properties.
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
