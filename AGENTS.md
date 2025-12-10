For auth and chat and for future changes we will do, we must avoid using Scan and sql.NullInt or pgtype. We must pass A pointer instead of sql sql.NullInt or pgtype. only use pgtype's extra features for things like partial updates or distinguishing unset values or to avoid extra boilerplate. // 1. Pass the struct type [T] to the function
// 2. pgx automatically creates the struct and maps fields by "db" tag
pois, err := pgx.CollectRows(rows, pgx.RowToStructByName[types.POIDetailedInfo]) Pro Tip: If you specifically want a slice of pointers ([]*types.POIDetailedInfo) instead of values, use pgx.RowToAddrOfStructByName:
// Returns []*types.POIDetailedInfo
pois, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[types.POIDetailedInfo])