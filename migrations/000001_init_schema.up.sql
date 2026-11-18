-- gen_random_uuid() is built into PostgreSQL 13+.

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(100) NOT NULL,
    email         VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role          VARCHAR(20)  NOT NULL DEFAULT 'customer' CHECK (role IN ('admin', 'customer')),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
-- Partial unique index: the email of a soft-deleted account can be reused.
CREATE UNIQUE INDEX uq_users_email ON users (email) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_deleted_at ON users (deleted_at);

CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash CHAR(64)    NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens (user_id);

CREATE TABLE categories (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(100) NOT NULL,
    slug       VARCHAR(120) NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_categories_slug ON categories (slug) WHERE deleted_at IS NULL;
CREATE INDEX idx_categories_deleted_at ON categories (deleted_at);

CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id UUID         NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    name        VARCHAR(200) NOT NULL,
    slug        VARCHAR(220) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    -- Money is stored as an integer amount of the smallest currency unit.
    price       BIGINT       NOT NULL CHECK (price >= 0),
    -- Last line of defence against overselling: stock can never go negative.
    stock       INTEGER      NOT NULL DEFAULT 0 CHECK (stock >= 0),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_products_slug ON products (slug) WHERE deleted_at IS NULL;
CREATE INDEX idx_products_category_id ON products (category_id);
CREATE INDEX idx_products_created_at ON products (created_at DESC);
CREATE INDEX idx_products_deleted_at ON products (deleted_at);

CREATE TABLE carts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cart_items (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id    UUID        NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    product_id UUID        NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    quantity   INTEGER     NOT NULL CHECK (quantity > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_cart_items_cart_product UNIQUE (cart_id, product_id)
);

CREATE TABLE orders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    order_number VARCHAR(40) NOT NULL UNIQUE,
    status       VARCHAR(20) NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PAID', 'PROCESSING', 'SHIPPED', 'COMPLETED', 'CANCELLED')),
    total_amount BIGINT      NOT NULL CHECK (total_amount >= 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_orders_user_created ON orders (user_id, created_at DESC);
CREATE INDEX idx_orders_status ON orders (status);

CREATE TABLE order_items (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id     UUID         NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    -- Nullable so a product row can disappear without touching order history.
    product_id   UUID REFERENCES products (id) ON DELETE SET NULL,
    -- Snapshot of the product at checkout time (intentional denormalization).
    product_name VARCHAR(200) NOT NULL,
    unit_price   BIGINT       NOT NULL CHECK (unit_price >= 0),
    quantity     INTEGER      NOT NULL CHECK (quantity > 0),
    subtotal     BIGINT       NOT NULL CHECK (subtotal >= 0)
);
CREATE INDEX idx_order_items_order_id ON order_items (order_id);

CREATE TABLE payments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID         NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    reference       VARCHAR(40)  NOT NULL UNIQUE,
    idempotency_key VARCHAR(100) NOT NULL UNIQUE,
    amount          BIGINT       NOT NULL CHECK (amount >= 0),
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'SUCCESS', 'FAILED', 'EXPIRED')),
    paid_at         TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ  NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_payments_order_id ON payments (order_id);
-- At most one active (PENDING) payment per order, enforced by the database.
CREATE UNIQUE INDEX uq_payments_pending_order ON payments (order_id) WHERE status = 'PENDING';
