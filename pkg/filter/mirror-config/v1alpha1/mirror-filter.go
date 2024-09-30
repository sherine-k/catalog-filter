package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	mmsemver "github.com/Masterminds/semver/v3"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/property"
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

type OperatorIndex struct {
	// Packages is a map that stores the packages in the operator catalog.
	// The key is the package name and the value is the corresponding declcfg.Package object.
	Packages map[string]declcfg.Package
	// Channels is a map that stores the channels for each package in the operator catalog.
	// The key is the package name and the value is a slice of declcfg.Channel objects.
	Channels     map[string][]declcfg.Channel
	ChannelNames map[string]sets.Set[string]
	// ChannelEntries is a map that stores the channel entries (Bundle names) for each channel and package in the operator catalog.
	// The first key is the package name, the second key is the channel name, and the third key is the bundle name (or channel entry name).
	// The value is the corresponding declcfg.ChannelEntry object.
	ChannelEntries map[string]map[string]map[string]declcfg.ChannelEntry
	// BundlesByPkgAndName is a map that stores the bundles for each package and bundle name in the operator catalog.
	// The first key is the package name, the second key is the bundle name, and the value is the corresponding declcfg.Bundle object.
	// This map allows quick access to the bundles based on the package and bundle name.
	BundlesByPkgAndName        map[string]map[string]declcfg.Bundle
	BundleVersionsByPkgAndName map[string]map[string]*mmsemver.Version
}

func WithLogger(log *logrus.Entry) FilterOption {
	return func(opts *filterOptions) {
		opts.Log = log
	}
}

func InFull(full bool) FilterOption {
	return func(opts *filterOptions) {
		opts.Full = full
	}
}

func nullLogger() *logrus.Entry {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return logrus.NewEntry(l)
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

	filteredFBC.Bundles = []declcfg.Bundle{}
	for _, bdl := range fbc.Bundles {
		if _, ok := f.chConfigs[bdl.Package]; ok {
			filteredFBC.Bundles = append(filteredFBC.Bundles, bdl)
		}
	}
	filteredFBC.Deprecations = []declcfg.Deprecation{}
	for _, d := range fbc.Deprecations {
		if _, ok := f.chConfigs[d.Package]; ok {
			filteredFBC.Deprecations = append(filteredFBC.Deprecations, d)
		}
	}
	filteredFBC.Others = []declcfg.Meta{}
	for _, meta := range fbc.Others {
		_, belongsToFilteredPackage := f.chConfigs[meta.Package]
		if meta.Package == "" || belongsToFilteredPackage {
			filteredFBC.Others = append(filteredFBC.Others, meta)
		}
	}
	return
}

func newOperatorIndex() OperatorIndex {
	operatorConfig := OperatorIndex{
		Packages:                   make(map[string]declcfg.Package),
		Channels:                   make(map[string][]declcfg.Channel),
		ChannelNames:               make(map[string]sets.Set[string]),
		ChannelEntries:             make(map[string]map[string]map[string]declcfg.ChannelEntry),
		BundlesByPkgAndName:        make(map[string]map[string]declcfg.Bundle),
		BundleVersionsByPkgAndName: make(map[string]map[string]*mmsemver.Version),
	}

	return operatorConfig
}
func GetIndex(cfg *declcfg.DeclarativeConfig) (OperatorIndex, error) {

	index := newOperatorIndex()

	for _, p := range cfg.Packages {
		index.Packages[p.Name] = p
	}

	for _, c := range cfg.Channels {
		index.Channels[c.Package] = append(index.Channels[c.Package], c)
		if _, ok := index.ChannelNames[c.Package]; !ok {
			index.ChannelNames[c.Package] = sets.New[string]()
		}
		index.ChannelNames[c.Package].Insert(c.Name)
		for _, e := range c.Entries {
			if _, ok := index.ChannelEntries[c.Package]; !ok {
				index.ChannelEntries[c.Package] = make(map[string]map[string]declcfg.ChannelEntry)
			}
			if _, ok := index.ChannelEntries[c.Package][c.Name]; !ok {
				index.ChannelEntries[c.Package][c.Name] = make(map[string]declcfg.ChannelEntry)
			}
			index.ChannelEntries[c.Package][c.Name][e.Name] = e
		}
	}

	for _, b := range cfg.Bundles {
		v, err := getBundleVersion(b)
		if err != nil {
			return OperatorIndex{}, err
		}
		if _, ok := index.BundlesByPkgAndName[b.Package]; !ok {
			index.BundlesByPkgAndName[b.Package] = make(map[string]declcfg.Bundle)
		}

		if _, ok := index.BundlesByPkgAndName[b.Package][b.Name]; !ok {
			index.BundlesByPkgAndName[b.Package][b.Name] = b
		}
		bundleVersions, ok := index.BundleVersionsByPkgAndName[b.Package]
		if !ok {
			bundleVersions = make(map[string]*mmsemver.Version)
		}
		bundleVersions[b.Name] = v
		index.BundleVersionsByPkgAndName[b.Package] = bundleVersions

	}

	return index, nil
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
		// for each package, set the default channel
		index, err := GetIndex(fbc)
		if err != nil {
			return filteredFBC, err
		}
		for i, pkg := range filteredFBC.Packages {
			pkgConfig := f.pkgConfigs[pkg.Name]
			if err := setDefaultChannel(&filteredFBC.Packages[i], pkgConfig, index.ChannelNames[pkg.Name]); err != nil {
				return nil, fmt.Errorf("invalid default channel configuration for package %q: %v", pkg.Name, err)
			}
			if f.opts.Full {
				// all channels for this package should be kept
				return filteredFBC, nil
			} else if f.pkgConfigs[pkg.Name].VersionRange != "" {
				// TODO: in each channel, keep only bundles within range
				// unless breaking the graph
				// keepBundles := map[string]sets.Set[string]{}
				// for i, fbcCh := range filteredFBC.Channels {
				// 	keepEntries := sets.New[string]()
				// }
			} else if len(f.pkgConfigs[pkg.Name].Channels) > 0 {
				// the work of keeping only the filtered channels
				// was already done by filterByPackageAndChannels

				keepBundles := map[string]sets.Set[string]{}
				for i, fbcCh := range filteredFBC.Channels {
					keepEntries := sets.New[string]()
					chConfig, ok := f.chConfigs[fbcCh.Package][fbcCh.Name]
					if !ok || chConfig.VersionRange == "" {
						// filtering by channel only:
						// what we need to do is to keep the channel head only
						filteringChannel, err := newChannel(fbcCh, f.opts.Log)
						if err != nil {
							return nil, err
						}
						fbcCh.Entries = []declcfg.ChannelEntry{index.ChannelEntries[fbcCh.Package][fbcCh.Name][filteringChannel.head.Name]}
						filteredFBC.Channels[i] = fbcCh
						// filteredFBC.Bundles = append(filteredFBC.Bundles, index.BundlesByPkgAndName[fbcCh.Package][filteringChannel.head.Name])
						if _, ok := keepBundles[fbcCh.Package]; !ok {
							keepBundles[fbcCh.Package] = sets.New[string]()
						}
						keepBundles[fbcCh.Package].Insert(filteringChannel.head.Name)
					} else if chConfig.VersionRange != "" {
						// filtering by channel and version range:
						// we need to keep only channel entries and bundles
						// within the range (without breaking graphs)
						versionRange, err := mmsemver.NewConstraint(chConfig.VersionRange)
						if err != nil {
							return nil, fmt.Errorf("error parsing version range: %v", err)
						}
						ch, err := newChannel(fbcCh, f.opts.Log)
						if err != nil {
							return nil, err
						}
						keepEntries = ch.filterByVersionRange(versionRange, index.BundleVersionsByPkgAndName[fbcCh.Package])
						if len(keepEntries) == 0 {
							return nil, fmt.Errorf("package %q channel %q has version range %q that results in an empty channel", fbcCh.Package, fbcCh.Name, chConfig.VersionRange)
						}
						filteredFBC.Channels[i].Entries = slices.DeleteFunc(filteredFBC.Channels[i].Entries, func(e declcfg.ChannelEntry) bool {
							return !keepEntries.Has(e.Name)
						})
						if _, ok := keepBundles[fbcCh.Package]; !ok {
							keepBundles[fbcCh.Package] = sets.New[string]()
						}
						keepBundles[fbcCh.Package] = keepBundles[fbcCh.Package].Union(keepEntries)

					}
				}
				filteredFBC.Bundles = slices.DeleteFunc(filteredFBC.Bundles, func(b declcfg.Bundle) bool {
					bundles, ok := keepBundles[b.Package]
					return ok && !bundles.Has(b.Name)
				})

				for i := range fbc.Deprecations {
					filteredFBC.Deprecations[i].Entries = slices.DeleteFunc(filteredFBC.Deprecations[i].Entries, func(e declcfg.DeprecationEntry) bool {
						if e.Reference.Schema == declcfg.SchemaBundle {
							bundles, ok := keepBundles[fbc.Deprecations[i].Package]
							return ok && !bundles.Has(e.Reference.Name)
						}
						if e.Reference.Schema == declcfg.SchemaChannel {
							channels, ok := index.ChannelNames[fbc.Deprecations[i].Package]
							return ok && !channels.Has(e.Reference.Name)
						}
						return false
					})
				}

			} else {
				// for each package, keep only the default channel
				filteredFBC.Channels = []declcfg.Channel{}
				filteredFBC.Bundles = []declcfg.Bundle{}
				for _, pkg := range filteredFBC.Packages {
					defaultChan := declcfg.Channel{
						Name:    pkg.DefaultChannel,
						Package: pkg.Name,
					}
					slices.SortFunc(index.Channels[pkg.Name], compareChannels)
					channelIndex, exists := slices.BinarySearchFunc(index.Channels[pkg.Name], defaultChan, compareChannels)
					if exists {
						filteredFBC.Channels = append(filteredFBC.Channels, index.Channels[pkg.Name][channelIndex])
					} else {
						return nil, fmt.Errorf("default channel %s not found for package %s", pkg.DefaultChannel, pkg.Name)
					}
				}
				// in each channel, keep only the head
				for ind, ch := range filteredFBC.Channels {
					filteringChannel, err := newChannel(ch, f.opts.Log)
					if err != nil {
						return nil, err
					}
					ch.Entries = []declcfg.ChannelEntry{index.ChannelEntries[ch.Package][ch.Name][filteringChannel.head.Name]}
					filteredFBC.Channels[ind] = ch
					filteredFBC.Bundles = append(filteredFBC.Bundles, index.BundlesByPkgAndName[ch.Package][filteringChannel.head.Name])
				}
			}
		}

	} else {
		// no filtering by package: all packages remain
		if f.opts.Full {
			return fbc, nil
		} else {
			filteredFBC.Packages = fbc.Packages
			filteredFBC.Others = fbc.Others
			filteredFBC.Deprecations = fbc.Deprecations
			index, err := GetIndex(fbc)
			if err != nil {
				return filteredFBC, err
			}
			// for each package, keep only the default channel
			for _, pkg := range fbc.Packages {
				defaultChan := declcfg.Channel{
					Name:    pkg.DefaultChannel,
					Package: pkg.Name,
				}
				slices.SortFunc(index.Channels[pkg.Name], compareChannels)
				channelIndex, exists := slices.BinarySearchFunc(index.Channels[pkg.Name], defaultChan, compareChannels)
				if exists {
					filteredFBC.Channels = append(filteredFBC.Channels, index.Channels[pkg.Name][channelIndex])
				} else {
					return nil, fmt.Errorf("default channel %s not found for package %s", pkg.DefaultChannel, pkg.Name)
				}
			}
			// within each remaining channel, keep only the head entry
			for ind, ch := range filteredFBC.Channels {
				filteringChannel, err := newChannel(ch, f.opts.Log)
				if err != nil {
					return nil, err
				}
				ch.Entries = []declcfg.ChannelEntry{index.ChannelEntries[ch.Package][ch.Name][filteringChannel.head.Name]}
				filteredFBC.Channels[ind] = ch
				filteredFBC.Bundles = append(filteredFBC.Bundles, index.BundlesByPkgAndName[ch.Package][filteringChannel.head.Name])
			}
		}
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

// func (f *mirrorFilter) filterCatalogInFull(_ context.Context, fbc *declcfg.DeclarativeConfig) (*declcfg.DeclarativeConfig, error) {
// 	if fbc == nil {
// 		return nil, nil
// 	}

// 	if len(f.pkgConfigs) != 0 {
// 		fbc = f.filterByPackage(fbc)
// 	}
// 	remainingChannels := make(map[string]sets.Set[string], len(fbc.Packages))
// 	for _, ch := range fbc.Channels {
// 		pkgChannels, ok := remainingChannels[ch.Package]
// 		if !ok {
// 			pkgChannels = sets.New[string]()
// 		}
// 		pkgChannels.Insert(ch.Name)
// 		remainingChannels[ch.Package] = pkgChannels
// 	}
// 	for i, pkg := range fbc.Packages {
// 		pkgConfig := f.pkgConfigs[pkg.Name]
// 		if err := setDefaultChannel(&fbc.Packages[i], pkgConfig, remainingChannels[pkg.Name]); err != nil {
// 			return nil, fmt.Errorf("invalid default channel configuration for package %q: %v", pkg.Name, err)
// 		}
// 	}

// 	getVersion := func(b declcfg.Bundle) (*mmsemver.Version, error) {
// 		for _, p := range b.Properties {
// 			if p.Type != property.TypePackage {
// 				continue
// 			}
// 			var pkg property.Package
// 			if err := json.Unmarshal(p.Value, &pkg); err != nil {
// 				return nil, err
// 			}
// 			return mmsemver.StrictNewVersion(pkg.Version)
// 		}
// 		return nil, fmt.Errorf("bundle %q in package %q has no package property", b.Name, b.Package)
// 	}

// 	versionMap := make(map[string]map[string]*mmsemver.Version)
// 	for _, b := range fbc.Bundles {
// 		v, err := getVersion(b)
// 		if err != nil {
// 			return nil, err
// 		}
// 		bundleVersions, ok := versionMap[b.Package]
// 		if !ok {
// 			bundleVersions = make(map[string]*mmsemver.Version)
// 		}
// 		bundleVersions[b.Name] = v
// 		versionMap[b.Package] = bundleVersions
// 	}

// 	keepBundles := map[string]sets.Set[string]{}
// 	for i, fbcCh := range fbc.Channels {
// 		keepEntries := sets.New[string]()
// 		chConfig, ok := f.chConfigs[fbcCh.Package][fbcCh.Name]
// 		if !ok || chConfig.VersionRange == "" {
// 			for _, e := range fbcCh.Entries {
// 				keepEntries.Insert(e.Name)
// 				keepEntries.Insert(e.Skips...)
// 				if e.Replaces != "" {
// 					keepEntries.Insert(e.Replaces)
// 				}
// 			}
// 		} else if chConfig.VersionRange == "" {
// 			// put only the channel head
// 			ch, err := newChannel(fbcCh, f.opts.Log)
// 			if err != nil {
// 				return nil, err
// 			}
// 			keepEntries.Insert(ch.head.Name)
// 		} else if chConfig.VersionRange != "" {
// 			versionRange, err := mmsemver.NewConstraint(chConfig.VersionRange)
// 			if err != nil {
// 				return nil, fmt.Errorf("error parsing version range: %v", err)
// 			}
// 			ch, err := newChannel(fbcCh, f.opts.Log)
// 			if err != nil {
// 				return nil, err
// 			}
// 			keepEntries = ch.filterByVersionRange(versionRange, versionMap[fbcCh.Package])
// 			if len(keepEntries) == 0 {
// 				return nil, fmt.Errorf("package %q channel %q has version range %q that results in an empty channel", fbcCh.Package, fbcCh.Name, chConfig.VersionRange)
// 			}
// 			fbc.Channels[i].Entries = slices.DeleteFunc(fbc.Channels[i].Entries, func(e declcfg.ChannelEntry) bool {
// 				return !keepEntries.Has(e.Name)
// 			})
// 		}

// 		if _, ok := keepBundles[fbcCh.Package]; !ok {
// 			keepBundles[fbcCh.Package] = sets.New[string]()
// 		}
// 		keepBundles[fbcCh.Package] = keepBundles[fbcCh.Package].Union(keepEntries)
// 	}

// 	fbc.Bundles = slices.DeleteFunc(fbc.Bundles, func(b declcfg.Bundle) bool {
// 		bundles, ok := keepBundles[b.Package]
// 		return ok && !bundles.Has(b.Name)
// 	})

// 	for i := range fbc.Deprecations {
// 		fbc.Deprecations[i].Entries = slices.DeleteFunc(fbc.Deprecations[i].Entries, func(e declcfg.DeprecationEntry) bool {
// 			if e.Reference.Schema == declcfg.SchemaBundle {
// 				bundles, ok := keepBundles[fbc.Deprecations[i].Package]
// 				return ok && !bundles.Has(e.Reference.Name)
// 			}
// 			if e.Reference.Schema == declcfg.SchemaChannel {
// 				channels, ok := remainingChannels[fbc.Deprecations[i].Package]
// 				return ok && !channels.Has(e.Reference.Name)
// 			}
// 			return false
// 		})
// 	}
// 	return fbc, nil
// }

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

func compareChannels(a, b declcfg.Channel) int {
	comparison := strings.Compare(a.Package, b.Package)
	if comparison != 0 {
		return comparison
	} else {
		return strings.Compare(a.Name, b.Name)
	}
}

func getBundleVersion(b declcfg.Bundle) (*mmsemver.Version, error) {
	for _, p := range b.Properties {
		if p.Type != property.TypePackage {
			continue
		}
		var pkg property.Package
		if err := json.Unmarshal(p.Value, &pkg); err != nil {
			return nil, err
		}
		return mmsemver.StrictNewVersion(pkg.Version)
	}
	return nil, fmt.Errorf("bundle %q in package %q has no package property", b.Name, b.Package)
}
