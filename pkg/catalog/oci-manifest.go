package catalog

import (
	"context"
	"os"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	filter "github.com/operator-framework/operator-registry/alpha/declcfg/filter/mirror-config/v1alpha1"
	"github.com/sherine-k/test-filter/pkg/api/v2alpha1"
	clog "github.com/sherine-k/test-filter/pkg/log"
)

var internalLog clog.PluggableLoggerInterface

type Manifest struct {
	Log clog.PluggableLoggerInterface
}

func New(log clog.PluggableLoggerInterface) ManifestInterface {
	internalLog = log
	return &Manifest{Log: log}
}

func setInternalLog(log clog.PluggableLoggerInterface) {
	if internalLog == nil {
		internalLog = log
	}
}

func (o Manifest) GetDeclarativeConfig(filePath string) (*declcfg.DeclarativeConfig, error) {
	setInternalLog(o.Log)
	return declcfg.LoadFS(context.Background(), os.DirFS(filePath))
}

func filterFromImageSetConfig(iscCatalogFilter v2alpha1.Operator) filter.FilterConfiguration {
	catFilter := filter.FilterConfiguration{
		TypeMeta: v1.TypeMeta{
			Kind:       "FilterConfiguration",
			APIVersion: "olm.operatorframework.io/v1alpha1",
		},
		Packages: []filter.Package{},
	}
	if len(iscCatalogFilter.Packages) > 0 {
		for _, op := range iscCatalogFilter.Packages {
			p := filter.Package{
				Name: op.Name,
			}
			if op.DefaultChannel != "" {
				p.DefaultChannel = op.DefaultChannel
			}
			if op.MinVersion != "" {
				p.VersionRange = ">=" + op.MinVersion
			}
			if op.MaxVersion != "" {
				p.VersionRange += " <=" + op.MaxVersion
			}
			if len(op.Channels) > 0 {
				p.Channels = []filter.Channel{}
				for _, ch := range op.Channels {
					filterChan := filter.Channel{
						Name: ch.Name,
					}

					if ch.MinVersion != "" {
						filterChan.VersionRange = ">=" + ch.MinVersion
					}
					if ch.MaxVersion != "" {
						filterChan.VersionRange += " <=" + ch.MaxVersion
					}
					p.Channels = append(p.Channels, filterChan)
				}
			}
			catFilter.Packages = append(catFilter.Packages, p)
		}
	}
	return catFilter
}

func (o Manifest) FilterCatalog(ctx context.Context, operatorCatalog declcfg.DeclarativeConfig, iscCatalogFilter v2alpha1.Operator) (*declcfg.DeclarativeConfig, error) {
	config := filterFromImageSetConfig(iscCatalogFilter)
	ctlgFilter := filter.NewMirrorFilter(config, []filter.FilterOption{filter.InFull(iscCatalogFilter.Full)}...)
	return ctlgFilter.FilterCatalog(ctx, &operatorCatalog)
}
