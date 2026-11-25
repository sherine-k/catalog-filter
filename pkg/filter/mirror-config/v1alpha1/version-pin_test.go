package v1alpha1

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reproduce OCPBUGS-138615 without requiring access to a remote catalog.
// The bundle names and versions are from the report; the replaces chain is
// synthetic. Build metadata distinguishes the rebuilds but not semver precedence.
func TestFilter_FilterCatalog_VersionPinReproducibility(t *testing.T) {
	for _, constraint := range []string{
		">=1.12.1 <=1.12.1", "1.12.1", "=1.12.1",
		">=1.12.1, <=1.12.1", "<=1.12.1 >=1.12.1",
		">=1.12.1+first <=1.12.1+last", ">=1.12.1 <=1.12.2-0",
		"1.12.1 || 9.0.0",
	} {
		t.Run(constraint, func(t *testing.T) {
			// Exhaust entry permutations, and independently rotate/reverse bundle
			// order. Repeated identical input must produce identical output; entry
			// order itself remains the caller's order, not a new canonical order.
			for _, order := range pinPermutations([]int{0, 1, 2, 3}) {
				for bundleOrder := range 8 {
					t.Run(fmt.Sprint(order, bundleOrder), func(t *testing.T) {
						input := versionPinCatalog()
						entries := slices.Clone(input.Channels[0].Entries)
						for i, index := range order {
							input.Channels[0].Entries[i] = entries[index]
						}
						bundles := slices.Clone(input.Bundles)
						for i := range bundles {
							input.Bundles[i] = bundles[(i+bundleOrder)%4]
						}
						if bundleOrder >= 4 {
							slices.Reverse(input.Bundles)
						}
						wantEntries := slices.Clone(input.Channels[0].Entries)
						wantNames := []string{}
						for _, b := range bundles {
							wantNames = append(wantNames, b.Name)
						}
						f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "web-terminal", Channels: []Channel{{Name: "fast", VersionRange: constraint}}}}})
						var previous *declcfg.DeclarativeConfig
						for range 2 {
							out, err := f.FilterCatalog(context.Background(), input)
							require.NoError(t, err)
							assert.ElementsMatch(t, wantNames, pinBundleNames(out))
							require.Len(t, out.Channels, 1)
							assert.Equal(t, wantEntries, out.Channels[0].Entries)
							ch, err := newChannel(out.Channels[0], nullLogger())
							require.NoError(t, err)
							assert.Equal(t, "web-terminal.v1.12.1-0.1745393748.p", ch.head.Name)
							model, err := declcfg.ConvertToModel(*out)
							require.NoError(t, err)
							modelHead, err := model["web-terminal"].Channels["fast"].Head()
							require.NoError(t, err)
							assert.Equal(t, ch.head.Name, modelHead.Name)
							if previous != nil {
								assert.Equal(t, previous, out)
							}
							previous = out
						}
					})
				}
			}
		})
	}
}

func pinPermutations(values []int) [][]int {
	if len(values) == 0 {
		return [][]int{{}}
	}
	var result [][]int
	for i, value := range values {
		rest := append(slices.Clone(values[:i]), values[i+1:]...)
		for _, tail := range pinPermutations(rest) {
			result = append(result, append([]int{value}, tail...))
		}
	}
	return result
}

func pinBundleNames(catalog *declcfg.DeclarativeConfig) []string {
	var names []string
	for _, bundle := range catalog.Bundles {
		names = append(names, bundle.Name)
	}
	return names
}

func versionPinCatalog() *declcfg.DeclarativeConfig {
	builds := []struct {
		name    string
		version string
	}{
		{"web-terminal.v1.12.1", "1.12.1"},
		{"web-terminal.v1.12.1-0.1742484227.p", "1.12.1+0.1742484227.p"},
		{"web-terminal.v1.12.1-0.1743657317.p", "1.12.1+0.1743657317.p"},
		{"web-terminal.v1.12.1-0.1745393748.p", "1.12.1+0.1745393748.p"},
	}
	catalog := &declcfg.DeclarativeConfig{
		Packages: []declcfg.Package{{Schema: declcfg.SchemaPackage, Name: "web-terminal", DefaultChannel: "fast"}},
		Channels: []declcfg.Channel{{Schema: declcfg.SchemaChannel, Name: "fast", Package: "web-terminal"}},
	}
	for i, build := range builds {
		entry := declcfg.ChannelEntry{Name: build.name}
		if i > 0 {
			entry.Replaces = builds[i-1].name
		}
		catalog.Channels[0].Entries = append(catalog.Channels[0].Entries, entry)
		catalog.Bundles = append(catalog.Bundles, declcfg.Bundle{
			Schema:     declcfg.SchemaBundle,
			Image:      "quay.io/example/" + build.name + ":latest",
			Name:       build.name,
			Package:    "web-terminal",
			Properties: propertiesForBundle("web-terminal", build.version),
		})
	}
	return catalog
}
