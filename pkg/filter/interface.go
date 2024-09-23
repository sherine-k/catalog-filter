package filter

import (
	"context"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
)

type CatalogFilter interface {
	FilterCatalog(ctx context.Context, fbc *declcfg.DeclarativeConfig) (*declcfg.DeclarativeConfig, error)
}

type MetaFilter interface {
	KeepMeta(meta *declcfg.Meta) bool
}

type MetaFilterFunc func(meta *declcfg.Meta) bool

func (f MetaFilterFunc) KeepMeta(meta *declcfg.Meta) bool { return f(meta) }
