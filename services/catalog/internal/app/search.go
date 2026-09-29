package app

import (
	"context"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// SearchService serves categories and public product search.
type SearchService struct {
	store SearchStore
}

// NewSearchService returns a SearchService.
func NewSearchService(store SearchStore) *SearchService { return &SearchService{store: store} }

// Categories returns the category tree.
func (s *SearchService) Categories(ctx context.Context) ([]domain.Category, error) {
	return s.store.Categories(ctx)
}

// Search validates p, applies defaults and runs the search.
func (s *SearchService) Search(ctx context.Context, p domain.SearchParams) (domain.Page[domain.ProductSummary], error) {
	p, err := p.Normalize()
	if err != nil {
		return domain.Page[domain.ProductSummary]{}, err
	}
	return s.store.SearchProducts(ctx, p)
}
