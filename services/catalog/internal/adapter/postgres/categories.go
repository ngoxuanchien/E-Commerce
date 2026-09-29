package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// Categories returns the two-level category tree ordered by position.
func (s *Store) Categories(ctx context.Context) ([]domain.Category, error) {
	rows, err := s.q.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	tops := []domain.Category{}
	index := map[uuid.UUID]int{}
	for _, r := range rows {
		if r.ParentID == nil {
			index[r.ID] = len(tops)
			tops = append(tops, domain.Category{ID: r.ID, Name: r.Name, Slug: r.Slug, Children: []domain.Category{}})
		}
	}
	for _, r := range rows {
		if r.ParentID == nil {
			continue
		}
		if i, ok := index[*r.ParentID]; ok {
			tops[i].Children = append(tops[i].Children,
				domain.Category{ID: r.ID, Name: r.Name, Slug: r.Slug, Children: []domain.Category{}})
		}
	}
	return tops, nil
}
