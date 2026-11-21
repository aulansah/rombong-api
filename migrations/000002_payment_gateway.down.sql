ALTER TABLE payments
    DROP COLUMN redirect_url,
    DROP COLUMN gateway_token,
    DROP COLUMN provider;
