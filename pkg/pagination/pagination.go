package pagination

type PageRequest struct {
	Limit  int
	Offset int
}

func (p PageRequest) Normalize(defaultLimit, maxLimit int) PageRequest {
	if p.Limit <= 0 {
		p.Limit = defaultLimit
	}
	if maxLimit > 0 && p.Limit > maxLimit {
		p.Limit = maxLimit
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}
