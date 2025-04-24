SELECT
    product_id,
    name AS "ProductName", 
    category,
    price,
    stock_count AS "Stock"
FROM
    products
WHERE
    product_id = 1
LIMIT 1; 
