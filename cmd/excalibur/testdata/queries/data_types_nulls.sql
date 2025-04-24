SELECT
    id,
    description,
    is_active,   -- Expecting false
    created_at,
    expiry_date, -- Expecting NULL
    settings,
    tag_list,
    nullable_int -- Expecting NULL
FROM
    data_types_test
WHERE
    id = 2
LIMIT 1;
