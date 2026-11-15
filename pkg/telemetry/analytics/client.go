package analytics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/posthog/posthog-go"
)

// Cap CLI exit delay on slow networks; dropping a queued event beats
// blocking the user's shell.
const flushTimeout = 2 * time.Second

// Injected at build time via -ldflags -X. Empty in local builds yields a noop client.
var posthogAPIKey = ""

const posthogEndpoint = "https://us.i.posthog.com"

var Dry = false

func NewClient() Client {
	if posthogAPIKey == "" {
		log.Debugf("analytics disabled: API key not configured")
		return NewNoopClient()
	}

	cfg, trace := posthogConfig(posthogEndpoint)
	phClient, err := posthog.NewWithConfig(posthogAPIKey, cfg)
	if err != nil {
		log.Debugf("failed to initialize analytics client: %v", err)
		return NewNoopClient()
	}

	return &client{phClient: phClient, flushTimeout: flushTimeout, trace: trace}
}

func posthogConfig(endpoint string) (posthog.Config, *phaseTrace) {
	transport, trace := newTracingTransport()
	return posthog.Config{
		Endpoint:     endpoint,
		DisableGeoIP: new(false),
		Transport:    transport,
		// Bounds Close as well, so no shutdown path can block the CLI.
		ShutdownTimeout: flushTimeout,
		Logger:          quietLogger{},
	}, trace
}

// quietLogger keeps the SDK off stderr, which machine consumers parse.
type quietLogger struct{}

func (quietLogger) Debugf(format string, args ...any) { log.Debugf("posthog: "+format, args...) }
func (quietLogger) Logf(format string, args ...any)   { log.Debugf("posthog: "+format, args...) }
func (quietLogger) Warnf(format string, args ...any)  { log.Debugf("posthog: "+format, args...) }
func (quietLogger) Errorf(format string, args ...any) { log.Debugf("posthog: "+format, args...) }

type client struct {
	phClient     posthog.Client
	flushTimeout time.Duration
	trace        *phaseTrace
	closeOnce    sync.Once
}

func (c *client) RecordEvent(event Event) {
	eventData, ok := event["event"]
	if !ok {
		return
	}

	machineID, _ := eventData[KeyMachineID].(string)
	eventType, _ := eventData[KeyType].(string)
	properties := buildProperties(event)

	if Dry {
		log.Infof(
			"analytics event: type=%s machine_id=%s properties=%v",
			eventType, machineID, properties,
		)
		return
	}

	if machineID == "" {
		log.Debugf("skipping event with empty machine_id: %s", eventType)
		return
	}

	if err := c.phClient.Enqueue(posthog.Capture{
		DistinctId: machineID,
		Event:      eventType,
		Properties: properties,
	}); err != nil {
		log.Debugf("error recording analytics event: %v", err)
	}
}

// Exclude reserved keys for event routing/identity.
func isReservedKey(k string) bool {
	return k == KeyType || k == KeyMachineID || k == KeyTimestamp
}

func buildProperties(event Event) posthog.Properties {
	properties := posthog.NewProperties()

	for k, v := range event["event"] {
		if isReservedKey(k) {
			continue
		}
		properties.Set(k, v)
	}

	for k, v := range event["user"] {
		if isReservedKey(k) {
			continue
		}
		properties.Set(k, v)
	}

	return properties
}

func (c *client) Flush() {
	if Dry {
		return
	}
	// The underlying client's Close drains the queue but can only be called once.
	c.closeOnce.Do(func() {
		// On deadline the SDK cancels the in-flight upload, so nothing keeps
		// running after Flush returns.
		ctx, cancel := context.WithTimeout(context.Background(), c.flushTimeout)
		defer cancel()
		switch err := c.phClient.CloseWithContext(ctx); {
		case err == nil:
		case errors.Is(err, context.DeadlineExceeded):
			log.Debugf(
				"analytics flush timed out after %s; dropping queued events (%s)",
				c.flushTimeout, c.trace.summary(),
			)
		default:
			log.Debugf("error flushing analytics client: %v", err)
		}
	})
}
