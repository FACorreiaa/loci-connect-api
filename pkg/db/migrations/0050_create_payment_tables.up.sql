-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS payments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL DEFAULT 'stripe',
    external_payment_id VARCHAR(255) UNIQUE, -- Stripe PaymentIntent ID
    type VARCHAR(50) NOT NULL, -- 'one_time', 'subscription'
    payment_method VARCHAR(50), -- 'card', etc.
    amount BIGINT NOT NULL, -- in cents
    currency VARCHAR(3) NOT NULL DEFAULT 'usd',
    status VARCHAR(50) NOT NULL, -- 'succeeded', 'pending', 'failed'
    description TEXT,
    metadata JSONB DEFAULT '{}'::jsonb,
    failure_reason TEXT,
    failed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trigger_set_payments_updated_at
BEFORE UPDATE ON payments
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS invoices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4 (),
    payment_id UUID REFERENCES payments (id) ON DELETE SET NULL,
    invoice_number VARCHAR(100) UNIQUE,
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    status VARCHAR(50) NOT NULL, -- 'paid', 'void', 'draft'
    pdf_url TEXT,
    line_items JSONB,
    issued_at TIMESTAMPTZ,
    paid_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trigger_set_invoices_updated_at
BEFORE UPDATE ON invoices
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS refunds (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4 (),
    payment_id UUID NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    stripe_refund_id VARCHAR(255) UNIQUE,
    amount_cents BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    reason TEXT,
    status VARCHAR(50) NOT NULL, -- 'succeeded', 'pending'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER trigger_set_refunds_updated_at
BEFORE UPDATE ON refunds
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS webhook_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4 (),
    event_id VARCHAR(255) UNIQUE NOT NULL, -- Stripe Event ID
    event_type VARCHAR(255) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Platform fees (optional, but good for tracking if using Connect or similar logic later)
CREATE TABLE IF NOT EXISTS platform_fees (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4 (),
    subscription_id UUID REFERENCES subscriptions (id) ON DELETE SET NULL,
    payment_id UUID REFERENCES payments (id) ON DELETE SET NULL,
    creator_id UUID REFERENCES users (id), -- If revenue share
    subscriber_id UUID REFERENCES users (id),
    gross_amount BIGINT NOT NULL,
    platform_fee_amount BIGINT NOT NULL,
    creator_amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    stripe_transfer_id VARCHAR(255),
    stripe_application_fee_id VARCHAR(255),
    status VARCHAR(50) NOT NULL DEFAULT 'completed',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd