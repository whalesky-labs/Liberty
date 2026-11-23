package legacydb

import (
	"database/sql"
	"math"
	"strconv"
)

type uint64Column uint64
type uint32Column uint32

func (column *uint64Column) Scan(value any) error {
	if column == nil {
		return ErrDatabase
	}
	number, err := unsignedColumn(value, 64)
	if err != nil {
		return err
	}
	*column = uint64Column(number)
	return nil
}

func (column *uint32Column) Scan(value any) error {
	if column == nil {
		return ErrDatabase
	}
	number, err := unsignedColumn(value, 32)
	if err != nil {
		return err
	}
	*column = uint32Column(number)
	return nil
}

func unsignedColumn(value any, bits int) (uint64, error) {
	if number, ok := value.(int64); ok {
		if number < 0 || bits == 32 && number > math.MaxUint32 {
			return 0, ErrDatabase
		}
		return uint64(number), nil
	}
	var text sql.NullString
	if text.Scan(value) != nil || !text.Valid {
		return 0, ErrDatabase
	}
	number, err := strconv.ParseUint(text.String, 10, bits)
	if err != nil {
		return 0, ErrDatabase
	}
	return number, nil
}
