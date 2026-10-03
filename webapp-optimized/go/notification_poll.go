package isureal

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

type notificationSubscriber struct {
	ID          int64     `json:"id"`
	UserID      string    `json:"user_id"`
	Region      string    `json:"region"`
	ConnectedAt time.Time `json:"connected_at"`
	result      chan notificationResult
}

type notificationResult struct {
	notification *getNotificationsResponse
	err          error
}

// One poller per registered handler. Only outstanding subscribers are retained;
// no authorization or notification state survives a poll or subscription.
type notificationPoller struct {
	db          *sqlx.DB
	mu          sync.Mutex
	nextID      int64
	subscribers map[int64]*notificationSubscriber
	cancel      context.CancelFunc
}

func newNotificationPoller(db *sqlx.DB) *notificationPoller {
	return &notificationPoller{db: db, subscribers: make(map[int64]*notificationSubscriber)}
}

func (p *notificationPoller) subscribe(user, region string, at time.Time) (*notificationSubscriber, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	s := &notificationSubscriber{ID: p.nextID, UserID: user, Region: region, ConnectedAt: at, result: make(chan notificationResult, 1)}
	p.subscribers[s.ID] = s
	if p.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		go p.run(ctx)
	}
	return s, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.removeLocked(s.ID)
	}
}

const notificationPollSQL = `
 SELECT s.id AS subscription_id,u.region AS user_region,u.secret_key_hash,
   n.id,n.posting_window_id,n.region,n.created_at
 FROM jsonb_to_recordset($1::jsonb) AS s(id bigint,user_id text,region text,connected_at timestamptz)
 LEFT JOIN users u ON u.id=s.user_id AND u.status='active'
 LEFT JOIN LATERAL (
   SELECT n.id,n.posting_window_id,w.region,n.created_at
   FROM notifications n JOIN posting_windows w ON w.id=n.posting_window_id
   WHERE n.user_id=u.id AND n.created_at>s.connected_at AND w.ended_at IS NULL
     AND u.region=s.region
   ORDER BY n.created_at,n.id LIMIT 1
 ) n ON true`

type notificationPollRow struct {
	SubscriptionID     int64      `db:"subscription_id"`
	Region             *string    `db:"user_region"`
	SecretKeyHash      *string    `db:"secret_key_hash"`
	ID                 *string    `db:"id"`
	PostingWindowID    *string    `db:"posting_window_id"`
	NotificationRegion *string    `db:"region"`
	CreatedAt          *time.Time `db:"created_at"`
}

func (p *notificationPoller) run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		p.mu.Lock()
		if ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		batch := make([]*notificationSubscriber, 0, len(p.subscribers))
		for _, s := range p.subscribers {
			batch = append(batch, s)
		}
		p.mu.Unlock()
		p.poll(ctx, batch)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *notificationPoller) poll(ctx context.Context, batch []*notificationSubscriber) {
	if len(batch) == 0 {
		return
	}
	data, err := json.Marshal(batch)
	var rows []notificationPollRow
	if err == nil {
		err = p.db.SelectContext(ctx, &rows, notificationPollSQL, string(data))
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		for _, s := range batch {
			p.deliver(s.ID, notificationResult{err: err})
		}
		return
	}
	for _, row := range rows {
		p.mu.Lock()
		s := p.subscribers[row.SubscriptionID]
		p.mu.Unlock()
		if s == nil {
			continue
		}
		if row.Region == nil || *row.Region != s.Region {
			p.deliver(s.ID, notificationResult{})
			continue
		}
		if _, err := imageSigningKey(*row.SecretKeyHash); err != nil {
			p.deliver(s.ID, notificationResult{})
			continue
		}
		if row.ID != nil {
			p.deliver(s.ID, notificationResult{notification: &getNotificationsResponse{
				ID: *row.ID, PostingWindowID: *row.PostingWindowID, Region: *row.NotificationRegion, CreatedAt: *row.CreatedAt,
			}})
		}
	}
}

func (p *notificationPoller) deliver(id int64, result notificationResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.subscribers[id]; s != nil {
		select {
		case s.result <- result:
			// Delivery is terminal. Slow HTTP writes must not keep polling.
			p.removeLocked(id)
		default:
		}
	}
}

// Caller holds mu. Unsubscribe remains safe after terminal delivery.
func (p *notificationPoller) removeLocked(id int64) {
	delete(p.subscribers, id)
	if len(p.subscribers) == 0 && p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}
