SELECT
    region_id,
    total_sales AS "TotalSales",
    manager_name AS "Manager"  
FROM
    region_summary
WHERE
    region_id = 'NORTH'
LIMIT 1;
