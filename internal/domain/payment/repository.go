package payment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBAL defines the methods that the repository needs to interact with the database.
// This interface allows for easier mocking and testing.
type DBAL interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Note: pgxpool.Pool implements this implicitly/explicitly via its methods.
// Use PgxPool interface for better testability as seen in prior tasks.
type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Actually, standard practice here is to just accept the Pool or the common interface.
// I'll stick to a concrete Repository struct holding the pool interface.

type Repository interface {
	CreatePayment(ctx context.Context, payment *Payment) error
	GetPaymentByID(ctx context.Context, id uuid.UUID) (*Payment, error)
	GetPaymentByExternalID(ctx context.Context, externalID string) (*Payment, error)
	UpdatePaymentStatus(ctx context.Context, id uuid.UUID, status string, failureReason *string) error
	GetUserPayments(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Payment, int, error)

	CreateInvoice(ctx context.Context, invoice *Invoice) error
	GetInvoiceByID(ctx context.Context, id uuid.UUID) (*Invoice, error)
	GetUserInvoices(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Invoice, int, error)
	LinkPaymentToInvoice(ctx context.Context, paymentID, invoiceID uuid.UUID) error

	CreateRefund(ctx context.Context, refund *Refund) error
	GetTotalRefundedAmount(ctx context.Context, paymentID uuid.UUID) (int64, error)

	RecordWebhookEvent(ctx context.Context, eventID, eventType string) error

	// Subscriptions (mapping logic)
	GetSubscriptionByStripeID(ctx context.Context, stripeSubID string) (*Subscription, error)
	GetSubscriptionByUserID(ctx context.Context, userID uuid.UUID) (*Subscription, error)
	UpdateSubscriptionStatus(ctx context.Context, id uuid.UUID, status string, start, end *time.Time) error
	UpsertSubscription(ctx context.Context, sub *Subscription) error
}

type repository struct {
	db PgxPool
}

func NewRepository(db PgxPool) Repository {
	return &repository{db: db}
}

// -- Models matching DB --

type Payment struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	Provider          string
	ExternalPaymentID *string
	Type              string
	PaymentMethod     *string
	Amount            int64
	Currency          string
	Status            string
	Description       *string
	Metadata          map[string]any
	FailureReason     *string
	FailedAt          *time.Time
	CompletedAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Invoice struct {
	ID            uuid.UUID
	PaymentID     *uuid.UUID
	InvoiceNumber *string
	UserID        uuid.UUID
	Amount        int64
	Currency      string
	Status        string
	PdfURL        *string
	LineItems     map[string]any // or []interface{}
	IssuedAt      *time.Time
	PaidAt        *time.Time
	CreatedAt     time.Time
}

type Refund struct {
	ID             uuid.UUID
	PaymentID      uuid.UUID
	StripeRefundID *string
	AmountCents    int64
	Currency       string
	Reason         *string
	Status         string
	CreatedAt      time.Time
}

type Subscription struct {
	ID                     uuid.UUID
	UserID                 uuid.UUID
	Plan                   string // e.g., 'free', 'pro'
	Status                 string
	StartDate              time.Time
	EndDate                *time.Time
	TrialEndDate           *time.Time
	ExternalProvider       string
	ExternalSubscriptionID *string
	ExternalCustomerID     *string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// -- Implementation --

func (r *repository) CreatePayment(ctx context.Context, p *Payment) error {
	query := `
        INSERT INTO payments (
            id, user_id, provider, external_payment_id, type, payment_method, 
            amount, currency, status, description, metadata, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW()
        )
    `
	// Use p.ID if present or generate new
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	_, err := r.db.Exec(ctx, query,
		p.ID, p.UserID, p.Provider, p.ExternalPaymentID, p.Type, p.PaymentMethod,
		p.Amount, p.Currency, p.Status, p.Description, p.Metadata,
	)
	return err
}

func (r *repository) GetPaymentByID(ctx context.Context, id uuid.UUID) (*Payment, error) {
	query := `SELECT id, user_id, provider, external_payment_id, type, payment_method, amount, currency, status, description, metadata, failure_reason, failed_at, completed_at, created_at, updated_at FROM payments WHERE id = $1`
	var p Payment
	var meta []byte
	err := r.db.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.UserID, &p.Provider, &p.ExternalPaymentID, &p.Type, &p.PaymentMethod,
		&p.Amount, &p.Currency, &p.Status, &p.Description, &meta,
		&p.FailureReason, &p.FailedAt, &p.CompletedAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if meta != nil {
		_ = json.Unmarshal(meta, &p.Metadata)
	}
	return &p, nil
}

func (r *repository) GetPaymentByExternalID(ctx context.Context, externalID string) (*Payment, error) {
	query := `SELECT id, user_id, provider, external_payment_id, type, payment_method, amount, currency, status, description, metadata, failure_reason, failed_at, completed_at, created_at, updated_at FROM payments WHERE external_payment_id = $1`
	var p Payment
	var meta []byte
	err := r.db.QueryRow(ctx, query, externalID).Scan(
		&p.ID, &p.UserID, &p.Provider, &p.ExternalPaymentID, &p.Type, &p.PaymentMethod,
		&p.Amount, &p.Currency, &p.Status, &p.Description, &meta,
		&p.FailureReason, &p.FailedAt, &p.CompletedAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if meta != nil {
		_ = json.Unmarshal(meta, &p.Metadata)
	}
	return &p, nil
}

func (r *repository) UpdatePaymentStatus(ctx context.Context, id uuid.UUID, status string, failureReason *string) error {
	query := `UPDATE payments SET status = $1, failure_reason = COALESCE($2, failure_reason), updated_at = NOW() WHERE id = $3`
	_, err := r.db.Exec(ctx, query, status, failureReason, id)
	return err
}

func (r *repository) GetUserPayments(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Payment, int, error) {
	// Total count
	var total int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE user_id = $1`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
        SELECT id, user_id, provider, external_payment_id, type, payment_method, amount, currency, status, description, metadata, created_at 
        FROM payments 
        WHERE user_id = $1 
        ORDER BY created_at DESC 
        LIMIT $2 OFFSET $3
    `
	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var payments []Payment
	for rows.Next() {
		var p Payment
		var meta []byte
		if err := rows.Scan(&p.ID, &p.UserID, &p.Provider, &p.ExternalPaymentID, &p.Type, &p.PaymentMethod, &p.Amount, &p.Currency, &p.Status, &p.Description, &meta, &p.CreatedAt); err != nil {
			return nil, 0, err
		}
		if meta != nil {
			_ = json.Unmarshal(meta, &p.Metadata)
		}
		payments = append(payments, p)
	}
	return payments, total, nil
}

func (r *repository) CreateInvoice(ctx context.Context, i *Invoice) error {
	query := `
        INSERT INTO invoices (
            id, payment_id, invoice_number, user_id, amount, currency, status, pdf_url, line_items, issued_at, paid_at, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW()
        )
    `
	if i.ID == uuid.Nil {
		i.ID = uuid.New()
	}
	_, err := r.db.Exec(ctx, query,
		i.ID, i.PaymentID, i.InvoiceNumber, i.UserID, i.Amount, i.Currency, i.Status, i.PdfURL, i.LineItems, i.IssuedAt, i.PaidAt,
	)
	return err
}

func (r *repository) GetInvoiceByID(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	query := `SELECT id, payment_id, invoice_number, user_id, amount, currency, status, pdf_url, line_items, issued_at, paid_at FROM invoices WHERE id = $1`
	var i Invoice
	var items []byte
	err := r.db.QueryRow(ctx, query, id).Scan(
		&i.ID, &i.PaymentID, &i.InvoiceNumber, &i.UserID, &i.Amount, &i.Currency, &i.Status, &i.PdfURL, &items, &i.IssuedAt, &i.PaidAt,
	)
	if err != nil {
		return nil, err
	}
	if items != nil {
		_ = json.Unmarshal(items, &i.LineItems)
	}
	return &i, nil
}

func (r *repository) GetUserInvoices(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Invoice, int, error) {
	var total int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM invoices WHERE user_id = $1`, userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
        SELECT id, payment_id, invoice_number, user_id, amount, currency, status, pdf_url, line_items, issued_at, paid_at
        FROM invoices 
        WHERE user_id = $1 
        ORDER BY created_at DESC 
        LIMIT $2 OFFSET $3
    `
	rows, err := r.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var invoices []Invoice
	for rows.Next() {
		var i Invoice
		var items []byte
		if err := rows.Scan(&i.ID, &i.PaymentID, &i.InvoiceNumber, &i.UserID, &i.Amount, &i.Currency, &i.Status, &i.PdfURL, &items, &i.IssuedAt, &i.PaidAt); err != nil {
			return nil, 0, err
		}
		if items != nil {
			_ = json.Unmarshal(items, &i.LineItems)
		}
		invoices = append(invoices, i)
	}
	return invoices, total, nil
}

func (r *repository) LinkPaymentToInvoice(ctx context.Context, paymentID, invoiceID uuid.UUID) error {
	query := `UPDATE invoices SET payment_id = $1 WHERE id = $2`
	_, err := r.db.Exec(ctx, query, paymentID, invoiceID)
	return err
}

func (r *repository) CreateRefund(ctx context.Context, refund *Refund) error {
	query := `
        INSERT INTO refunds (
            id, payment_id, stripe_refund_id, amount_cents, currency, reason, status, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, NOW(), NOW()
        )
    `
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	_, err := r.db.Exec(ctx, query,
		refund.ID, refund.PaymentID, refund.StripeRefundID, refund.AmountCents, refund.Currency, refund.Reason, refund.Status,
	)
	return err
}

func (r *repository) GetTotalRefundedAmount(ctx context.Context, paymentID uuid.UUID) (int64, error) {
	var total *int64
	// COALESCE to return 0 if null
	query := `SELECT SUM(amount_cents) FROM refunds WHERE payment_id = $1 AND status = 'succeeded'`
	err := r.db.QueryRow(ctx, query, paymentID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

func (r *repository) RecordWebhookEvent(ctx context.Context, eventID, eventType string) error {
	query := `INSERT INTO webhook_events (event_id, event_type) VALUES ($1, $2)`
	_, err := r.db.Exec(ctx, query, eventID, eventType)
	return err
}

// Subscriptions
func (r *repository) GetSubscriptionByStripeID(ctx context.Context, stripeSubID string) (*Subscription, error) {
	query := `SELECT id, user_id, external_subscription_id, status FROM subscriptions WHERE external_subscription_id = $1`
	var s Subscription
	err := r.db.QueryRow(ctx, query, stripeSubID).Scan(&s.ID, &s.UserID, &s.ExternalSubscriptionID, &s.Status)
	return &s, err
}

func (r *repository) GetSubscriptionByUserID(ctx context.Context, userID uuid.UUID) (*Subscription, error) {
	// Note: the subscriptions table has no external_customer_id column, so it is
	// intentionally not selected here (selecting it fails with SQLSTATE 42703).
	query := `
		SELECT id, user_id, plan, status, start_date, end_date, trial_end_date,
		       external_provider, external_subscription_id, created_at, updated_at
		FROM subscriptions WHERE user_id = $1
	`
	var s Subscription
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&s.ID, &s.UserID, &s.Plan, &s.Status, &s.StartDate, &s.EndDate, &s.TrialEndDate,
		&s.ExternalProvider, &s.ExternalSubscriptionID, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // No subscription found
		}
		return nil, err
	}
	return &s, nil
}

func (r *repository) UpdateSubscriptionStatus(ctx context.Context, id uuid.UUID, status string, start, end *time.Time) error {
	// Only updating needed fields
	query := `UPDATE subscriptions SET status = $1, start_date = COALESCE($2, start_date), end_date = $3, updated_at = NOW() WHERE id = $4`
	_, err := r.db.Exec(ctx, query, status, start, end, id)
	return err
}

func (r *repository) UpsertSubscription(ctx context.Context, sub *Subscription) error {
	query := `
        INSERT INTO subscriptions (
            id, user_id, plan, status, start_date, end_date, trial_end_date, 
            external_provider, external_subscription_id, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW()
        )
        ON CONFLICT (user_id) DO UPDATE SET
            plan = EXCLUDED.plan,
            status = EXCLUDED.status,
            start_date = EXCLUDED.start_date,
            end_date = EXCLUDED.end_date,
            trial_end_date = EXCLUDED.trial_end_date,
            external_provider = EXCLUDED.external_provider,
            external_subscription_id = EXCLUDED.external_subscription_id,
            updated_at = NOW()
    `
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	_, err := r.db.Exec(ctx, query,
		sub.ID, sub.UserID, sub.Plan, sub.Status, sub.StartDate, sub.EndDate, sub.TrialEndDate,
		sub.ExternalProvider, sub.ExternalSubscriptionID,
	)
	return err
}
