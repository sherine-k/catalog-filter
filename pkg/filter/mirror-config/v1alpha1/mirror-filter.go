package v1alpha1

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	mmsemver "github.com/Masterminds/semver/v3"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/sherine-k/catalog-filter/pkg/filter"
)

type filterOptions struct {
	Log  *logrus.Entry
	Full bool
}

type FilterOption func(*filterOptions)

type mirrorFilter struct {
	pkgConfigs map[string]Package
	chConfigs  map[string]map[string]Channel
	opts       filterOptions
}

func WithLogger(log *logrus.Entry) FilterOption {
	return func(opts *filterOptions) {
		opts.Log = log
	}
}

func nullLogger() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return logrus.NewEntry(l)
}

func InFull(full bool) FilterOption {
	return func(opts *filterOptions) {
		opts.Full = full
	}
}

func NewMirrorFilter(config FilterConfiguration, filterOpts ...FilterOption) filter.CatalogFilter {
	opts := filterOptions{
		Log: nullLogger(),
	}
	for _, opt := range filterOpts {
		opt(&opts)
	}
	pkgConfigs := make(map[string]Package, len(config.Packages))
	chConfigs := make(map[string]map[string]Channel, len(config.Packages))
	for _, pkg := range config.Packages {
		pkgConfigs[pkg.Name] = pkg
		pkgChannels, ok := chConfigs[pkg.Name]
		if !ok {
			pkgChannels = make(map[string]Channel)
		}
		for _, ch := range pkg.Channels {
			pkgChannels[ch.Name] = ch
		}
		chConfigs[pkg.Name] = pkgChannels
	}
	return &mirrorFilter{
		pkgConfigs: pkgConfigs,
		chConfigs:  chConfigs,
		opts:       opts,
	}
}

// OLM's channel traversal logic is basically:
// * Find the channel head. The channel head is the node that no other entry replaces or skips . If there is more than one of these, fail with "multiple channel heads". If there are 0, also fail because there's a cycle.
// * Follow the linear replaces chain from the channel head and build a set (let's call in inChain) of those bundle names
// * For each bundle on the replaces chain, add that bundle's skips names to the inChain set.
// * Make a set called allEntries containing the names of entry in the channel.
// * Subtract inChain from allEntries . If items remain in allEntries, fail (there are some dangling bundles)
func (f *mirrorFilter) FilterCatalog(ctx context.Context, fbc *declcfg.DeclarativeConfig) (*declcfg.DeclarativeConfig, error) {
	if fbc == nil {
		return nil, nil
	}
	filteredFBC := &declcfg.DeclarativeConfig{}
	if len(f.pkgConfigs) != 0 {
		// keep in FBC only packages, channels and bundles
		// that belong to the filtered packages
		f.filterByPackageAndChannels(fbc, filteredFBC)
	} else {
		filteredFBC = fbc
	}
	catalogIndex, err := indexFromDeclCfg(filteredFBC)
	if err != nil {
		return filteredFBC, err
	}
	for pkgIndex, pkg := range filteredFBC.Packages {
		pkgConfig, exists := f.pkgConfigs[pkg.Name]
		if exists {
			if err := setDefaultChannel(&pkg, pkgConfig, catalogIndex.ChannelNames[pkg.Name]); err != nil {
				return nil, fmt.Errorf("invalid default channel configuration for package %q: %v", pkg.Name, err)
			}
			// TODO: not sure the following line is necessary
			filteredFBC.Packages[pkgIndex].DefaultChannel = pkg.DefaultChannel

			if (len(pkgConfig.Channels) == 0 && !f.opts.Full) && len(pkgConfig.SelectedBundles) == 0 {
				if err = keepPackageDefaultChannel(filteredFBC, pkg, catalogIndex); err != nil {
					return nil, fmt.Errorf("failure in filtering default channel for package %q: %v", pkg.Name, err)
				}
			} //len(pkgConfig.Channels) >0 : this is already covered by filterByPackageAndChannels
		} else {
			if !f.opts.Full {
				if err = keepPackageDefaultChannel(filteredFBC, pkg, catalogIndex); err != nil {
					return nil, fmt.Errorf("failure in filtering default channel for package %q: %v", pkg.Name, err)
				}
			} // if f.opts.Full, all channels need to remain, so no filtering needed here
		}
	}
	keepBundles := map[string]sets.Set[string]{}
	for channelIndex, ch := range filteredFBC.Channels {
		versionRange := f.chConfigs[ch.Package][ch.Name].VersionRange
		if versionRange == "" && f.pkgConfigs[ch.Package].VersionRange != "" {
			versionRange = f.pkgConfigs[ch.Package].VersionRange
		}
		switch {
		case f.opts.Full && versionRange != "":
			return nil, fmt.Errorf("Full: true cannot be mixed with versionRange")
		case f.opts.Full && len(f.pkgConfigs[ch.Package].SelectedBundles) > 0:
			return nil, fmt.Errorf("Full: true cannot be mixed with filtering by bundle selection")
		case len(f.pkgConfigs[ch.Package].SelectedBundles) > 0 && versionRange != "":
			return nil, fmt.Errorf("filtering by versionRange cannot be mixed with filtering by bundle selection")
		case len(f.pkgConfigs[ch.Package].SelectedBundles) > 0:
			if _, ok := keepBundles[ch.Package]; !ok {
				keepBundles[ch.Package] = sets.New[string]()
			}
			keepBundles[ch.Package].Insert(bundleNames(f.pkgConfigs[ch.Package].SelectedBundles)...)
			filteredFBC.Channels[channelIndex].Entries = slices.DeleteFunc(filteredFBC.Channels[channelIndex].Entries, func(e declcfg.ChannelEntry) bool {
				for _, selectedEntry := range f.pkgConfigs[ch.Package].SelectedBundles {
					if e.Name == selectedEntry.Name {
						return false
					}
				}
				return true
			})
			// verify the filtered channel is still valid
			_, err := newChannel(filteredFBC.Channels[channelIndex], f.opts.Log)
			if err != nil {
				return nil, fmt.Errorf("filtering on the selected bundles leads to invalidating channel %q for package %q: %v", ch.Name, ch.Package, err)
			}
		case f.opts.Full:
			for _, entry := range ch.Entries {
				if _, ok := keepBundles[ch.Package]; !ok {
					keepBundles[ch.Package] = sets.New[string]()
				}
				keepBundles[ch.Package].Insert(entry.Name)
			}
		case versionRange != "":
			keepEntries := sets.New[string]()
			rangeConstraint, err := mmsemver.NewConstraint(versionRange)
			if err != nil {
				return nil, fmt.Errorf("error parsing version range: %v", err)
			}
			filteringChannel, err := newChannel(ch, f.opts.Log)
			if err != nil {
				return nil, err
			}
			keepEntries = filteringChannel.filterByVersionRange(rangeConstraint, catalogIndex.BundleVersionsByPkgAndName[ch.Package])
			if len(keepEntries) == 0 {
				return nil, fmt.Errorf("package %q channel %q has version range %q that results in an empty channel", ch.Package, ch.Name, versionRange)
			}
			filteredFBC.Channels[channelIndex].Entries = slices.DeleteFunc(filteredFBC.Channels[channelIndex].Entries, func(e declcfg.ChannelEntry) bool {
				return !keepEntries.Has(e.Name)
			})
			if _, ok := keepBundles[ch.Package]; !ok {
				keepBundles[ch.Package] = sets.New[string]()
			}
			keepBundles[ch.Package] = keepBundles[ch.Package].Union(keepEntries)
		default:
			filteredChannel, chHead, err := f.filterChannelHead(ch, catalogIndex)
			if err != nil {
				return nil, fmt.Errorf("package %q channel %q unable to filter head of channel: %v", ch.Package, ch.Name, err)
			}
			filteredFBC.Channels[channelIndex] = filteredChannel
			if _, ok := keepBundles[ch.Package]; !ok {
				keepBundles[ch.Package] = sets.New[string]()
			}
			keepBundles[ch.Package] = keepBundles[ch.Package].Insert(chHead)
		}
	}
	if len(keepBundles) > 0 {
		filteredFBC.Bundles = []declcfg.Bundle{}
		for pkg, bundles := range keepBundles {
			for b := range bundles {
				bun, exists := catalogIndex.BundlesByPkgAndName[pkg][b]
				if exists {
					filteredFBC.Bundles = append(filteredFBC.Bundles, bun)
				}
			}
		}
		slices.SortFunc(filteredFBC.Bundles, compareBundles)

		filterDeprecations(filteredFBC, catalogIndex, keepBundles)
	}
	return filteredFBC, nil
}

func (f *mirrorFilter) KeepMeta(meta *declcfg.Meta) bool {
	if len(f.chConfigs) == 0 {
		return false
	}

	packageName := meta.Package
	if meta.Schema == "olm.package" {
		packageName = meta.Name
	}

	_, ok := f.chConfigs[packageName]
	return ok
}

func filterDeprecations(fbc *declcfg.DeclarativeConfig, index operatorIndex, keptBundles map[string]sets.Set[string]) *declcfg.DeclarativeConfig {
	for i := range fbc.Deprecations {
		fbc.Deprecations[i].Entries = slices.DeleteFunc(fbc.Deprecations[i].Entries, func(e declcfg.DeprecationEntry) bool {
			if e.Reference.Schema == declcfg.SchemaBundle {
				bundles, ok := keptBundles[fbc.Deprecations[i].Package]
				return ok && !bundles.Has(e.Reference.Name)
			}
			if e.Reference.Schema == declcfg.SchemaChannel {
				channels, ok := index.ChannelNames[fbc.Deprecations[i].Package]
				return ok && !channels.Has(e.Reference.Name)
			}
			return false
		})
	}
	return fbc
}
func (f *mirrorFilter) filterChannelHead(ch declcfg.Channel, index operatorIndex) (declcfg.Channel, string, error) {
	filteringChannel, err := newChannel(ch, f.opts.Log)
	if err != nil {
		return declcfg.Channel{}, "", err
	}
	ch.Entries = []declcfg.ChannelEntry{index.ChannelEntries[ch.Package][ch.Name][filteringChannel.head.Name]}
	return ch, filteringChannel.head.Name, nil
}

func setDefaultChannel(pkg *declcfg.Package, pkgConfig Package, channels sets.Set[string]) error {

	// If both the FBC and package config leave the default channel unspecified, then we don't need to do anything.
	if pkg.DefaultChannel == "" && pkgConfig.DefaultChannel == "" {
		return nil
	}

	// If the default channel was specified in the filter configuration, then we need to check if it exists after filtering.
	// If it does, then we update the model's default channel to the specified channel. Otherwise, we error.
	if pkgConfig.DefaultChannel != "" {
		if !channels.Has(pkgConfig.DefaultChannel) {
			return fmt.Errorf("specified default channel override %q does not exist in the filtered output", pkgConfig.DefaultChannel)
		}
		pkg.DefaultChannel = pkgConfig.DefaultChannel
		return nil
	}

	// At this point, we know that the default channel was not configured in the filter configuration for this package.
	// If the original default channel does not exist after filtering, error
	if !channels.Has(pkg.DefaultChannel) {
		return fmt.Errorf("the default channel %q was filtered out, a new default channel must be configured for this package", pkg.DefaultChannel)
	}
	return nil
}

func (f *mirrorFilter) filterByPackageAndChannels(fbc, filteredFBC *declcfg.DeclarativeConfig) {
	filteredFBC.Packages = []declcfg.Package{}
	for _, pkg := range fbc.Packages {
		if _, ok := f.chConfigs[pkg.Name]; ok {
			filteredFBC.Packages = append(filteredFBC.Packages, pkg)
		}
	}
	filteredFBC.Channels = []declcfg.Channel{}
	for _, ch := range fbc.Channels {
		chSet, foundPackage := f.chConfigs[ch.Package]
		if foundPackage {
			if len(chSet) > 0 {
				_, foundChannel := chSet[ch.Name]
				if foundChannel {
					filteredFBC.Channels = append(filteredFBC.Channels, ch)
				}
			} else {
				filteredFBC.Channels = append(filteredFBC.Channels, ch)
			}
		}
	}
	if len(filteredFBC.Channels) == 0 {
		filteredFBC.Channels = nil
	}
	filteredFBC.Bundles = []declcfg.Bundle{}
	for _, bdl := range fbc.Bundles {
		if _, ok := f.chConfigs[bdl.Package]; ok {
			filteredFBC.Bundles = append(filteredFBC.Bundles, bdl)
		}
	}
	if len(filteredFBC.Bundles) == 0 {
		filteredFBC.Bundles = nil
	}
	filteredFBC.Deprecations = []declcfg.Deprecation{}
	for _, d := range fbc.Deprecations {
		if _, ok := f.chConfigs[d.Package]; ok {
			filteredFBC.Deprecations = append(filteredFBC.Deprecations, d)
		}
	}
	if len(filteredFBC.Deprecations) == 0 {
		filteredFBC.Deprecations = nil
	}
	filteredFBC.Others = []declcfg.Meta{}
	for _, meta := range fbc.Others {
		_, belongsToFilteredPackage := f.chConfigs[meta.Package]
		if meta.Package == "" || belongsToFilteredPackage {
			filteredFBC.Others = append(filteredFBC.Others, meta)
		}
	}
	if len(filteredFBC.Others) == 0 {
		filteredFBC.Others = nil
	}
	return
}

func keepPackageDefaultChannel(fbc *declcfg.DeclarativeConfig, pkg declcfg.Package, index operatorIndex) error {
	defaultChan := declcfg.Channel{
		Name:    pkg.DefaultChannel,
		Package: pkg.Name,
	}
	slices.SortFunc(index.Channels[pkg.Name], compareChannels)
	channelIndex, exists := slices.BinarySearchFunc(index.Channels[pkg.Name], defaultChan, compareChannels)
	if exists {
		if fbc.Channels == nil {
			fbc.Channels = []declcfg.Channel{index.Channels[pkg.Name][channelIndex]}
		} else if len(index.Channels[pkg.Name]) == 0 {
			fbc.Channels = append(fbc.Channels, index.Channels[pkg.Name][channelIndex])
		} else {
			fbc.Channels = slices.DeleteFunc(fbc.Channels, func(ch declcfg.Channel) bool {
				if ch.Package == pkg.Name {
					return ch.Name != index.Channels[pkg.Name][channelIndex].Name
				}
				return false
			})
		}
	} else {
		return fmt.Errorf("default channel %s not found for package %s", pkg.DefaultChannel, pkg.Name)
	}
	return nil
}

func compareChannels(a, b declcfg.Channel) int {
	comparison := strings.Compare(a.Package, b.Package)
	if comparison != 0 {
		return comparison
	} else {
		return strings.Compare(a.Name, b.Name)
	}
}

func compareBundles(a, b declcfg.Bundle) int {
	comparison := strings.Compare(a.Package, b.Package)
	if comparison != 0 {
		return comparison
	} else {
		return strings.Compare(a.Name, b.Name)
	}
}

func bundleNames(bundles []SelectedBundle) []string {
	bundleNames := []string{}
	for _, bundle := range bundles {
		bundleNames = append(bundleNames, bundle.Name)
	}
	return bundleNames
}
