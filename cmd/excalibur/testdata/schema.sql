DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS region_summary;
DROP TABLE IF EXISTS data_types_test;

CREATE TABLE products (
    product_id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    category VARCHAR(50),
    price DECIMAL(10, 2),
    stock_count INT,
    last_updated TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO products (name, category, price, stock_count) VALUES
('Laptop Pro', 'Electronics', 1200.50, 50),
('Wireless Mouse', 'Accessories', 25.99, 200),
('Office Chair', 'Furniture', 150.00, 30);

CREATE TABLE region_summary (
    region_id VARCHAR(10) PRIMARY KEY,
    total_sales DECIMAL(12, 2),
    manager_name VARCHAR(100)
);

INSERT INTO region_summary (region_id, total_sales, manager_name) VALUES
('NORTH', 55000.75, 'Alice Smith'),
('SOUTH', 48200.00, 'Bob Jones');

CREATE TABLE data_types_test (
    id INT PRIMARY KEY,
    description TEXT,
    is_active BOOLEAN,
    created_at TIMESTAMPTZ,
    expiry_date DATE,
    settings JSONB,
    tag_list TEXT[],
    nullable_int INT
);

INSERT INTO data_types_test (id, description, is_active, created_at, expiry_date, settings, tag_list, nullable_int) VALUES
(1, 'Active Record', true, '2024-01-15 10:30:00+02', '2025-12-31', '{"feature_x": true, "level": 10}', ARRAY['alpha', 'beta'], 101),
(2, 'Inactive Record with Nulls', false, '2023-11-01 15:00:00Z', null, '{"feature_x": false, "level": 5}', ARRAY['gamma'], null),
(3, 'Record with Null JSON/Array', true, '2025-03-20 08:00:00+00', '2024-06-30', null, null, 202);
