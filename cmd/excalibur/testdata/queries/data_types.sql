SELECT
    id,
    description,
    is_active,
    created_at,  -- TIMESTAMPTZ
    expiry_date, -- DATE
    settings,    -- JSONB
    tag_list,    -- TEXT[]
    nullable_int
FROM
    data_types_test
WHERE
    id = 1
LIMIT 1;
