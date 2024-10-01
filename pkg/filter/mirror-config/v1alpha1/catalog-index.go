package v1alpha1

import (
	mmsemver "github.com/Masterminds/semver/v3"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"k8s.io/apimachinery/pkg/util/sets"
)

type operatorIndex struct {
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

func indexFromDeclCfg(cfg *declcfg.DeclarativeConfig) (operatorIndex, error) {

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
			return operatorIndex{}, err
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
