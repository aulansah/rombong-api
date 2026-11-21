-- Payment gateway support (Midtrans Snap). Existing rows are simulated payments.
ALTER TABLE payments
    ADD COLUMN provider      VARCHAR(20)  NOT NULL DEFAULT 'simulated'
        CHECK (provider IN ('simulated', 'midtrans')),
    ADD COLUMN gateway_token VARCHAR(100),
    ADD COLUMN redirect_url  TEXT;
