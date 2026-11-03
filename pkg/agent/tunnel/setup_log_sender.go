package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sender defaults.
const (
	defaultCleanupTimeout = 5 * time.Second
	defaultCallTimeout    = 10 * time.Second
	defaultPollInterval   = 500 * time.Millisecond
	maxRetryBackoff       = 5 * time.Second
	flushRetryPause       = 50 * time.Millisecond
	// batchReserve keeps room for the request envelope inside one page.
	batchReserve = 4 * 1024
)

// SetupLogSource supplies a producer's committed records. See
// workspacelogs.StoreSource.
type SetupLogSource interface {
	ReadAfter(runID, producerID string, after uint64, maxBytes int) ([]workspacelogs.Record, error)
}

// SetupLogSenderOptions configures a SetupLogSender.
type SetupLogSenderOptions struct {
	Client     TunnelClient
	Source     SetupLogSource
	RunID      string
	ProducerID string
	// Acks stores the acknowledged position so a restart resumes from it. When
	// nil the position lives in memory only.
	Acks *workspacelogs.AckStore
	// Hop names the position kept in Acks. Defaults to HopUpstream.
	Hop workspacelogs.Hop
	// CleanupTimeout bounds Flush. CallTimeout bounds one RPC.
	CleanupTimeout time.Duration
	CallTimeout    time.Duration
	PollInterval   time.Duration
}

// SetupLogSender mirrors one producer's records from a source store to the
// next host. Records stay in the source spool; the sender only remembers how
// far the peer has acknowledged, so a stalled peer never blocks the producer
// and never grows memory.
type SetupLogSender struct {
	opts   SetupLogSenderOptions
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}
	// drainLock admits one drain at a time and can be waited on with a context.
	drainLock chan struct{}

	disabled   atomic.Bool
	incomplete atomic.Bool

	mu       sync.Mutex
	acked    uint64
	inflight context.CancelFunc
}

// NewSetupLogSender starts the background worker. Call Close when done.
func NewSetupLogSender(ctx context.Context, opts SetupLogSenderOptions) *SetupLogSender {
	if opts.Hop == "" {
		opts.Hop = workspacelogs.HopUpstream
	}
	if opts.CleanupTimeout <= 0 {
		opts.CleanupTimeout = defaultCleanupTimeout
	}
	if opts.CallTimeout <= 0 {
		opts.CallTimeout = defaultCallTimeout
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	s := &SetupLogSender{
		opts:      opts,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
		drainLock: make(chan struct{}, 1),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	if opts.Acks != nil {
		if st, err := opts.Acks.Load(s.ackKey()); err == nil {
			s.acked = st.Acked
			s.incomplete.Store(st.Gapped)
		}
	}
	go s.worker()

	return s
}

// Notify tells the sender new records are available. It never blocks; wake-ups
// coalesce into one.
func (s *SetupLogSender) Notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Acked returns the highest sequence the peer acknowledged.
func (s *SetupLogSender) Acked() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.acked
}

// Disabled reports that the peer cannot take records (older peer or rejected
// batch). The source spool is intact.
func (s *SetupLogSender) Disabled() bool { return s.disabled.Load() }

// Close stops the background worker.
func (s *SetupLogSender) Close() {
	s.cancel()
	<-s.done
}

// Flush makes a bounded final drain. It always uses its own cleanup deadline,
// even when ctx is already canceled, and reports whether every retained record
// was acknowledged without gaps.
func (s *SetupLogSender) Flush(ctx context.Context) bool {
	if s.disabled.Load() {
		return false
	}
	cleanup, cancel := cleanupContext(ctx, s.opts.CleanupTimeout)
	defer cancel()
	s.cancelInflight()
	if !s.lock(cleanup) {
		return false
	}
	defer s.unlock()
	for !s.drainOnce(cleanup) {
		if s.disabled.Load() {
			return false
		}
		select {
		case <-cleanup.Done():
			return false
		case <-time.After(flushRetryPause):
		}
	}

	return !s.incomplete.Load() && !s.disabled.Load()
}

// drainOnce returns true when the source is exhausted or the peer is lost.
func (s *SetupLogSender) drainOnce(ctx context.Context) bool {
	done, err := s.drain(ctx)

	return err == nil && done || s.disabled.Load()
}

// cleanupContext bounds a final drain by its own deadline, whether or not the
// parent is already canceled.
func cleanupContext(
	parent context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if parent.Err() != nil {
		parent = context.WithoutCancel(parent)
	}

	return context.WithTimeout(parent, timeout)
}

func (s *SetupLogSender) lock(ctx context.Context) bool {
	select {
	case s.drainLock <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *SetupLogSender) unlock() { <-s.drainLock }

func (s *SetupLogSender) cancelInflight() {
	s.mu.Lock()
	cancel := s.inflight
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *SetupLogSender) worker() {
	defer close(s.done)
	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()
	backoff := time.Duration(0)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		ok, err := s.workerDrain()
		if !ok {
			return
		}
		backoff = nextBackoff(backoff, err)
		if backoff > 0 && !s.sleep(backoff) {
			return
		}
	}
}

// workerDrain runs one drain. ok is false when the worker should exit.
func (s *SetupLogSender) workerDrain() (bool, error) {
	if s.disabled.Load() || !s.lock(s.ctx) {
		return false, nil
	}
	defer s.unlock()
	_, err := s.drain(s.ctx)

	return true, err
}

func (s *SetupLogSender) sleep(d time.Duration) bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func nextBackoff(cur time.Duration, err error) time.Duration {
	switch {
	case err == nil:
		return 0
	case cur == 0:
		return 100 * time.Millisecond
	default:
		return min(cur*2, maxRetryBackoff)
	}
}

var errNoProgress = errors.New("peer acknowledged no new records")

// drain sends batches until the source has nothing past the acknowledged
// position. It returns true when caught up.
func (s *SetupLogSender) drain(ctx context.Context) (bool, error) {
	for {
		if s.disabled.Load() {
			return false, nil
		}
		records, err := s.opts.Source.ReadAfter(
			s.opts.RunID, s.opts.ProducerID, s.Acked(), workspacelogs.MaxPageBytes-batchReserve,
		)
		if errors.Is(err, workspacelogs.ErrSourceGap) {
			// The local spool lost the next records; nothing can be mirrored past it.
			s.incomplete.Store(true)
			s.disabled.Store(true)

			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read source: %w", err)
		}
		if len(records) == 0 {
			return true, nil
		}
		if err = s.send(ctx, records); err != nil {
			return false, err
		}
	}
}

func (s *SetupLogSender) send(ctx context.Context, records []workspacelogs.Record) error {
	req, err := buildRequest(s.opts.RunID, s.opts.ProducerID, records)
	if err != nil {
		s.incomplete.Store(true)
		s.disabled.Store(true)

		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.opts.CallTimeout)
	s.setInflight(cancel)
	defer func() {
		s.setInflight(nil)
		cancel()
	}()
	resp, err := s.opts.Client.AppendSetupLogs(callCtx, req)
	if status.Code(err) == codes.Unimplemented {
		s.disabled.Store(true)

		return nil
	}
	if err != nil {
		return err
	}

	return s.apply(req, resp)
}

func (s *SetupLogSender) ackKey() workspacelogs.AckKey {
	return workspacelogs.AckKey{RunID: s.opts.RunID, ProducerID: s.opts.ProducerID, Hop: s.opts.Hop}
}

func (s *SetupLogSender) setInflight(cancel context.CancelFunc) {
	s.mu.Lock()
	s.inflight = cancel
	s.mu.Unlock()
}

func buildRequest(
	runID, producerID string, records []workspacelogs.Record,
) (*AppendSetupLogsRequest, error) {
	req := &AppendSetupLogsRequest{
		SchemaVersion: workspacelogs.SchemaVersion,
		RunId:         runID,
		ProducerId:    producerID,
		FirstSequence: records[0].Sequence,
		LastSequence:  records[len(records)-1].Sequence,
	}
	for i := range records {
		data, err := json.Marshal(&records[i])
		if err != nil {
			return nil, fmt.Errorf("encode record: %w", err)
		}
		req.Records = append(req.Records, data)
	}

	return req, nil
}

// apply moves the acknowledged position by the peer's answer. The position
// never passes what this request carried.
func (s *SetupLogSender) apply(req *AppendSetupLogsRequest, resp *AppendSetupLogsResponse) error {
	accepted := min(resp.GetAcceptedSequence(), req.GetLastSequence())
	switch resp.GetResult() {
	case SetupLogResult_SETUP_LOG_RESULT_ACCEPTED:
		return s.advance(accepted)
	case SetupLogResult_SETUP_LOG_RESULT_RETENTION_GAP:
		s.incomplete.Store(true)
		last := min(resp.GetGapLastSequence(), req.GetLastSequence())
		if s.opts.Acks != nil {
			_ = s.opts.Acks.RecordGap(s.ackKey(), resp.GetGapFirstSequence(), last)
		}

		return s.advance(max(accepted, last))
	default:
		// Rejected or unknown: retrying the same bytes cannot succeed.
		s.incomplete.Store(true)
		s.disabled.Store(true)

		return nil
	}
}

func (s *SetupLogSender) advance(acked uint64) error {
	s.mu.Lock()
	moved := acked > s.acked
	if moved {
		s.acked = acked
	}
	s.mu.Unlock()
	if !moved {
		return errNoProgress
	}
	if s.opts.Acks != nil {
		return s.opts.Acks.Advance(s.ackKey(), acked)
	}

	return nil
}

// SetupLogForwarderOptions configures a SetupLogForwarder.
type SetupLogForwarderOptions struct {
	Client TunnelClient
	Source SetupLogSource
	RunID  string
	Acks   *workspacelogs.AckStore
	// CleanupTimeout bounds one Flush across every producer.
	CleanupTimeout time.Duration
	CallTimeout    time.Duration
	PollInterval   time.Duration
}

// SetupLogForwarder is the intermediate mode for the provider host. It sends
// the original records received from containers upstream with unchanged run,
// producer and sequence identity, and tracks the caller's acknowledged position
// separately from what its own store accepted.
type SetupLogForwarder struct {
	opts SetupLogForwarderOptions
	ctx  context.Context

	mu      sync.Mutex
	senders map[string]*SetupLogSender
	closed  bool
}

// NewSetupLogForwarder returns a forwarder whose senders live until ctx ends
// or Close is called.
func NewSetupLogForwarder(ctx context.Context, opts SetupLogForwarderOptions) *SetupLogForwarder {
	return &SetupLogForwarder{ctx: ctx, opts: opts, senders: map[string]*SetupLogSender{}}
}

// Notify is the Sink's OnAccepted hook: records of producerID are now in the
// local store and can go upstream. It never blocks.
func (f *SetupLogForwarder) Notify(producerID string, _ uint64) {
	if s := f.sender(producerID); s != nil {
		s.Notify()
	}
}

// Flush drains every producer upstream within one cleanup budget and reports
// whether the caller holds everything the provider holds. A failure at this
// hop is reported even though the provider's own store accepted the records.
func (f *SetupLogForwarder) Flush(ctx context.Context) bool {
	timeout := f.opts.CleanupTimeout
	if timeout <= 0 {
		timeout = defaultCleanupTimeout
	}
	budget, cancel := cleanupContext(ctx, timeout)
	defer cancel()

	f.mu.Lock()
	senders := make([]*SetupLogSender, 0, len(f.senders))
	for _, s := range f.senders {
		senders = append(senders, s)
	}
	f.mu.Unlock()

	var (
		wg       sync.WaitGroup
		failures atomic.Int32
	)
	for _, s := range senders {
		wg.Go(func() {
			if !s.Flush(budget) {
				failures.Add(1)
			}
		})
	}
	wg.Wait()

	return failures.Load() == 0
}

// Close stops every sender.
func (f *SetupLogForwarder) Close() {
	f.mu.Lock()
	f.closed = true
	senders := f.senders
	f.senders = map[string]*SetupLogSender{}
	f.mu.Unlock()
	for _, s := range senders {
		s.Close()
	}
}

func (f *SetupLogForwarder) sender(producerID string) *SetupLogSender {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	if s, ok := f.senders[producerID]; ok {
		return s
	}
	if len(f.senders) >= workspacelogs.MaxProducers {
		return nil
	}
	s := NewSetupLogSender(f.ctx, SetupLogSenderOptions{
		Client: f.opts.Client, Source: f.opts.Source, RunID: f.opts.RunID,
		ProducerID: producerID, Acks: f.opts.Acks, Hop: workspacelogs.HopUpstream,
		CleanupTimeout: f.opts.CleanupTimeout, CallTimeout: f.opts.CallTimeout,
		PollInterval: f.opts.PollInterval,
	})
	f.senders[producerID] = s

	return s
}
