package v1alpha1

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/property"

	filter_package "github.com/sherine-k/catalog-filter/pkg/filter"
)

func TestFilter_KeepMeta(t *testing.T) {
	tests := []struct {
		name     string
		filter   filter_package.MetaFilter
		meta     *declcfg.Meta
		expected bool
	}{
		{
			name:     "NoFilter_Package",
			filter:   NewMirrorFilter(FilterConfiguration{}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaPackage, Name: "foo"},
			expected: false,
		},
		{
			name:     "NoFilter_Channel",
			filter:   NewMirrorFilter(FilterConfiguration{}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaChannel, Package: "foo"},
			expected: false,
		},
		{
			name:     "NoFilter_Bundle",
			filter:   NewMirrorFilter(FilterConfiguration{}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaBundle, Package: "foo"},
			expected: false,
		},
		{
			name:     "NoFilter_Deprecation",
			filter:   NewMirrorFilter(FilterConfiguration{}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaDeprecation, Package: "foo"},
			expected: false,
		},
		{
			name:     "NoFilter_Other",
			filter:   NewMirrorFilter(FilterConfiguration{}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: "other", Package: "foo"},
			expected: false,
		},
		{
			name:     "KeepFooBar_Package",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "foo"}, {Name: "bar"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaPackage, Name: "foo"},
			expected: true,
		},
		{
			name:     "KeepFooBar_Channel",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "foo"}, {Name: "bar"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaChannel, Package: "foo"},
			expected: true,
		},
		{
			name:     "KeepFooBar_Bundle",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "foo"}, {Name: "bar"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaBundle, Package: "foo"},
			expected: true,
		},
		{
			name:     "KeepFooBar_Deprecation",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "foo"}, {Name: "bar"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaDeprecation, Package: "foo"},
			expected: true,
		},
		{
			name:     "KeepFooBar_Other",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "foo"}, {Name: "bar"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: "other", Package: "foo"},
			expected: true,
		},
		{
			name:     "KeepBarBaz_Package",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "bar"}, {Name: "baz"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaPackage, Name: "foo"},
			expected: false,
		},
		{
			name:     "KeepBarBaz_Channel",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "bar"}, {Name: "baz"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaChannel, Package: "foo"},
			expected: false,
		},
		{
			name:     "KeepBarBaz_Bundle",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "bar"}, {Name: "baz"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaBundle, Package: "foo"},
			expected: false,
		},
		{
			name:     "KeepBarBaz_Deprecation",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "bar"}, {Name: "baz"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: declcfg.SchemaDeprecation, Package: "foo"},
			expected: false,
		},
		{
			name:     "KeepBarBaz_Other",
			filter:   NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "bar"}, {Name: "baz"}}}).(filter_package.MetaFilter),
			meta:     &declcfg.Meta{Schema: "other", Package: "foo"},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.filter.KeepMeta(tt.meta)
			require.Equal(t, tt.expected, actual)
		})
	}
}

//go:embed testdata/declarative_configs
var declCfgFS embed.FS

func TestFilter_FilterCatalog(t *testing.T) {
	type testCase struct {
		name          string
		config        FilterConfiguration
		filterOptions []FilterOption
		in            *declcfg.DeclarativeConfig
		assertion     func(*testing.T, *declcfg.DeclarativeConfig, error)
	}

	testCases := []testCase{
		{
			name:   "WHEN empty config, nil fbc THEN Returns nil fbc",
			config: FilterConfiguration{},
			in:     nil,
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Nil(t, actual)
				assert.NoError(t, err)
			},
		},
		{
			name:   "WHEN empty config, empty fbc THEN Returns empty fbc",
			config: FilterConfiguration{},
			in:     &declcfg.DeclarativeConfig{},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Equal(t, &declcfg.DeclarativeConfig{}, actual)
				assert.NoError(t, err)
			},
		},
		{
			name:   "WHEN empty config THEN Returns all packages with all channels and their heads",
			config: FilterConfiguration{},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 3, len(actual.Packages))
				assert.Equal(t, 5, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.8.4-0.1655690146.p"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.9.1-0.1664967752.p"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.11.0-mas"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.51.0-1"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "devworkspace-operator.v0.19.1-0.1682321189.p"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:          "WHEN empty config AND full:true THEN Returns input fbc",
			config:        FilterConfiguration{},
			in:            loadDeclarativeConfig(t),
			filterOptions: []FilterOption{InFull(true)},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 5, len(actual.Channels))
				assert.Equal(t, 38, len(actual.Bundles))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package without channel filtering THEN Returns 1 package with its default channel and head bundle",
			config: FilterConfiguration{Packages: []Package{{Name: "3scale-operator"}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 3, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.8.4-0.1655690146.p"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.9.1-0.1664967752.p"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.11.0-mas"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package with direct versionRange filtering THEN Returns that package with its default channel filtered by versionRange",
			config: FilterConfiguration{Packages: []Package{{Name: "3scale-operator", VersionRange: ">=0.10.0-mas"}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 2, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.10.0-mas"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.11.0-mas"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package by channel no versionRange THEN Returns 1 package with specified channel and its head",
			config: FilterConfiguration{Packages: []Package{{Name: "jaeger-product", Channels: []Channel{{Name: "stable"}}}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 1, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.51.0-1"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:          "WHEN filter on 1 package, full, without channel filtering THEN Returns that package with all its channels and bundles",
			config:        FilterConfiguration{Packages: []Package{{Name: "3scale-operator"}}},
			filterOptions: []FilterOption{InFull(true)},
			in:            loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 3, len(actual.Channels))
				assert.Equal(t, 16, len(actual.Bundles))

				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:          "WHEN filter on 1 package, full, with channel filtering THEN Returns that package with all bundles of filtered channels",
			config:        FilterConfiguration{Packages: []Package{{Name: "3scale-operator", DefaultChannel: "threescale-2.11", Channels: []Channel{{Name: "threescale-2.11"}}}}},
			filterOptions: []FilterOption{InFull(true)},
			in:            loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 1, len(actual.Channels))
				assert.Equal(t, 11, len(actual.Channels[0].Entries))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package, channel filtering and defaultChannel THEN Returns that package with new defaultChannel and its head",
			config: FilterConfiguration{Packages: []Package{{Name: "3scale-operator", DefaultChannel: "threescale-2.12", Channels: []Channel{{Name: "threescale-2.12"}}}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 1, len(actual.Channels))
				assert.Equal(t, 1, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.9.1-0.1664967752.p"
				}))
				assert.Equal(t, "threescale-2.12", actual.Channels[0].Name)
				assert.Equal(t, 1, len(actual.Channels[0].Entries))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 2 packages THEN Returns 2 packages, all their channels and their resp. heads",
			config: FilterConfiguration{Packages: []Package{{Name: "jaeger-product"}, {Name: "3scale-operator"}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 2, len(actual.Packages))
				assert.Equal(t, 4, len(actual.Channels))
				assert.Equal(t, 4, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.51.0-1"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.11.0-mas"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.8.4-0.1655690146.p"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.9.1-0.1664967752.p"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package with channel and minVer filtering THEN Returns 1 package, 1 channel and all bundles from min to head",
			config: FilterConfiguration{Packages: []Package{{Name: "jaeger-product", Channels: []Channel{{Name: "stable", VersionRange: ">=1.47.1-5"}}}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 2, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.51.0-1"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.47.1-5"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package, 2 channels (1 with maxVersion filtering) THEN Returns 1 package, 2 channels (1 head + all bundles till max)",
			config: FilterConfiguration{Packages: []Package{{Name: "3scale-operator", Channels: []Channel{{Name: "threescale-mas"}, {Name: "threescale-2.12", VersionRange: "<=0.8.0+0.1634606167.p"}}}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 2, len(actual.Channels))
				assert.Equal(t, 3, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.8.0-0.1634606167.p"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.8.0"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.11.0-mas"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package, 1 channel with versionRange THEN Returns 1 package, 1 channel, all bundles within range",
			config: FilterConfiguration{Packages: []Package{{Name: "jaeger-product", Channels: []Channel{{Name: "stable", VersionRange: ">=1.34.1-5 <=1.42.0-5"}}}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 1, len(actual.Channels))
				assert.Equal(t, 3, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.34.1-5"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.42.0-5"
				}))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "jaeger-operator.v1.42.0-5-0.1687199951.p"
				}))

				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		{
			name:   "WHEN filter on 1 package, bundle filtering THEN Returns 1 package all channels containing selected bundles",
			config: FilterConfiguration{Packages: []Package{{Name: "3scale-operator", SelectedBundles: []SelectedBundle{{Name: "3scale-operator.v0.9.1-0.1664967752.p"}}}}},
			in:     loadDeclarativeConfig(t),
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(actual.Packages))
				assert.Equal(t, 2, len(actual.Channels))
				assert.Equal(t, 1, len(actual.Bundles))
				assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
					return b.Name == "3scale-operator.v0.9.1-0.1664967752.p"
				}))
				_, validationError := declcfg.ConvertToModel(*actual)
				assert.NoError(t, validationError)
			},
		},
		// {
		// 	name:   "filter on 3scale, 1 channel min&max filtering",
		// 	config: FilterConfiguration{Packages: []Package{{Name: "3scale-operator", Channels: []Channel{{Name: "threescale-mas", VersionRange: ">=0.9.1 <=0.10.0-mas"}}}}},
		// 	in:     loadDeclarativeConfig(t),
		// 	assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
		// 		assert.NoError(t, err)
		// 		assert.Equal(t, 1, len(actual.Packages))
		// 		assert.Equal(t, 1, len(actual.Channels))
		// 		assert.Equal(t, 3, len(actual.Bundles))
		// 		assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
		// 			return b.Name == "3scale-operator.v0.10.0-mas"
		// 		}))
		// 		assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
		// 			return b.Name == "3scale-operator.v0.9.1"
		// 		}))
		// 		assert.True(t, slices.ContainsFunc(actual.Bundles, func(b declcfg.Bundle) bool {
		// 			return b.Name == "3scale-operator.v0.9.1-0.1664967752.p"
		// 		}))

		// 		_, validationError := declcfg.ConvertToModel(*actual)
		// 		assert.NoError(t, validationError)
		// 	},
		// },
		{
			name: "WHEN filter has invalid version range THEN Returns error",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1", Channels: []Channel{{Name: "ch1", VersionRange: "something-isnt-right"}}},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1"}},
				Channels: []declcfg.Channel{{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b1"}}}},
				Bundles:  []declcfg.Bundle{{Name: "b1", Package: "pkg1", Properties: propertiesForBundle("pkg1", "1.0.0")}},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Nil(t, actual)
				assert.Error(t, err)
				assert.ErrorContains(t, err, "error parsing version range")
			},
		},
		{
			name: "WHEN input FBC has invalid fbc channel THEN Returns error",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1", Channels: []Channel{{Name: "ch1", VersionRange: ">=1.0.0 <2.0.0"}}},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1"}},
				Channels: []declcfg.Channel{{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{
					{Name: "b1", Replaces: "b0"},
					{Name: "b0", Replaces: "b1"},
				}}},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Nil(t, actual)
				assert.Error(t, err)
				assert.ErrorContains(t, err, "no channel heads found")
			},
		},
		{
			name: "WHEN range excludes all channel entries from default channel THEN Returns error",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1", Channels: []Channel{{Name: "ch1", VersionRange: ">100.0.0"}}},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch1"}},
				Channels: []declcfg.Channel{{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{
					{Name: "b1", Replaces: "b0"},
					{Name: "b0"},
				}}},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Nil(t, actual)
				assert.Error(t, err)
				assert.ErrorContains(t, err, "empty channel")
			},
		},
		{
			name: "WHEN filter 1 package default channel unspecified THEN Returns 1 package with default channel unchanged",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1"},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch1"}},
				Channels: []declcfg.Channel{
					{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b1", Replaces: "b0"}}},
					{Name: "ch2", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b3", Replaces: "b2"}}},
				},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Equal(t, &declcfg.DeclarativeConfig{
					Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch1"}},
					Channels: []declcfg.Channel{
						{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b1", Replaces: "b0"}}},
						{Name: "ch2", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b3", Replaces: "b2"}}},
					},
					Bundles: []declcfg.Bundle{},
				}, actual)
				assert.NoError(t, err)
			},
		},
		{
			name: "WHEN filter 1 package by non-default channel AND new DefaultChannel unspecified THEN Returns error",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1", Channels: []Channel{{Name: "ch2"}}},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch1"}},
				Channels: []declcfg.Channel{
					{Name: "ch1", Package: "pkg1"},
					{Name: "ch2", Package: "pkg1"},
				},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Nil(t, actual)
				assert.Error(t, err)
				assert.ErrorContains(t, err, `invalid default channel configuration for package "pkg1": the default channel "ch1" was filtered out, a new default channel must be configured for this package`)
			},
		},
		{
			name: "WHEN filter 1 package by non-default channel AND new DefaultChannel specified THEN Returns 1 package 1 selected new default channel and 1 head",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1", DefaultChannel: "ch2", Channels: []Channel{{Name: "ch2"}}},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch1"}},
				Channels: []declcfg.Channel{
					{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b1", Replaces: "b0"}}},
					{Name: "ch2", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b3", Replaces: "b2"}}},
				},
				Bundles: []declcfg.Bundle{{Name: "b3", Package: "pkg1", Properties: propertiesForBundle("pkg1", "2.0.0")}, {Name: "b1", Package: "pkg1", Properties: propertiesForBundle("pkg1", "1.0.0")}},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Equal(t, &declcfg.DeclarativeConfig{
					Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch2"}},
					Channels: []declcfg.Channel{
						{Name: "ch2", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b3", Replaces: "b2"}}},
					},
					Bundles: []declcfg.Bundle{{Name: "b3", Package: "pkg1", Properties: propertiesForBundle("pkg1", "2.0.0")}},
				}, actual)
				assert.NoError(t, err)
			},
		},
		{
			name: "WHEN filter 1 package by a channel AND that channel not configured as default THEN Returns error",
			config: FilterConfiguration{Packages: []Package{
				{Name: "pkg1", DefaultChannel: "ch2", Channels: []Channel{{Name: "ch1"}}},
			}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1", DefaultChannel: "ch1"}},
				Channels: []declcfg.Channel{
					{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b1", Replaces: "b0"}}},
					{Name: "ch2", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b3", Replaces: "b2"}}},
				},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Nil(t, actual)
				assert.Error(t, err)
				assert.ErrorContains(t, err, `invalid default channel configuration for package "pkg1": specified default channel override "ch2" does not exist in the filtered output`)
			},
		},
		{
			name: "WHEN filter 1 package and 1 channel THEN Returns deprecation entries for remaining package, channel and bundles ONLY",
			config: FilterConfiguration{Packages: []Package{{
				Name:     "pkg1",
				Channels: []Channel{{Name: "ch1"}},
			}}},
			in: &declcfg.DeclarativeConfig{
				Packages: []declcfg.Package{{Name: "pkg1"}},
				Channels: []declcfg.Channel{
					{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b2", Replaces: "b1", Skips: []string{"b0"}}, {Name: "b1"}}},
					{Name: "ch2", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b5", Replaces: "b4", Skips: []string{"b3"}}, {Name: "b4"}}},
				},
				Bundles: []declcfg.Bundle{
					// Pkg1 bundles
					{Name: "b1", Package: "pkg1", Properties: propertiesForBundle("pkg1", "0.1.0")},
					{Name: "b2", Package: "pkg1", Properties: propertiesForBundle("pkg1", "0.2.0")},
					{Name: "b3", Package: "pkg1", Properties: propertiesForBundle("pkg1", "3.0.0")},
					{Name: "b4", Package: "pkg1", Properties: propertiesForBundle("pkg1", "4.0.0")},
					{Name: "b5", Package: "pkg1", Properties: propertiesForBundle("pkg1", "5.0.0")},
				},
				Deprecations: []declcfg.Deprecation{{
					Package: "pkg1",
					Entries: []declcfg.DeprecationEntry{
						{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaPackage}},
						{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaChannel, Name: "ch1"}},
						{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaChannel, Name: "ch2"}},
						{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaBundle, Name: "b1"}},
						{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaBundle, Name: "b2"}},
						{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaBundle, Name: "b4"}},
					},
				}},
				Others: []declcfg.Meta{{Name: "global"}},
			},
			assertion: func(t *testing.T, actual *declcfg.DeclarativeConfig, err error) {
				assert.Equal(t, &declcfg.DeclarativeConfig{
					Packages: []declcfg.Package{{Name: "pkg1"}},
					Channels: []declcfg.Channel{
						{Name: "ch1", Package: "pkg1", Entries: []declcfg.ChannelEntry{{Name: "b2", Replaces: "b1", Skips: []string{"b0"}}}},
					},
					Bundles: []declcfg.Bundle{
						// Pkg1 bundles
						{Name: "b2", Package: "pkg1", Properties: propertiesForBundle("pkg1", "0.2.0")},
					},
					Deprecations: []declcfg.Deprecation{{
						Package: "pkg1",
						Entries: []declcfg.DeprecationEntry{
							{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaPackage}},
							{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaChannel, Name: "ch1"}},
							{Reference: declcfg.PackageScopedReference{Schema: declcfg.SchemaBundle, Name: "b2"}},
						},
					}},
					Others: []declcfg.Meta{{Name: "global"}},
				}, actual)
				assert.NoError(t, err)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "TODO") {
				t.Skip("TODO")
				return
			}
			f := NewMirrorFilter(tc.config, tc.filterOptions...)
			out, err := f.FilterCatalog(context.Background(), tc.in)
			tc.assertion(t, out, err)
		})
	}
}

func TestFilter_FilterCatalog_WithLogger(t *testing.T) {
	logOutput := &bytes.Buffer{}
	log := logrus.New()
	log.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableQuote: true})
	log.SetOutput(logOutput)
	withLogger := WithLogger(logrus.NewEntry(log))
	f := NewMirrorFilter(FilterConfiguration{Packages: []Package{
		{Name: "pkg", Channels: []Channel{{Name: "ch", VersionRange: ">=1.0.0 <2.0.0"}}},
	}}, withLogger)

	out, err := f.FilterCatalog(context.Background(), &declcfg.DeclarativeConfig{
		Packages: []declcfg.Package{{Name: "pkg"}},
		Channels: []declcfg.Channel{{Name: "ch", Package: "pkg", Entries: []declcfg.ChannelEntry{
			{Name: "b2", Skips: []string{"b1"}},
			{Name: "b1"},
		}}},
		Bundles: []declcfg.Bundle{
			{Name: "b1", Package: "pkg", Properties: propertiesForBundle("pkg", "1.0.0")},
			{Name: "b2", Package: "pkg", Properties: propertiesForBundle("pkg", "2.0.0")},
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, &declcfg.DeclarativeConfig{
		Packages: []declcfg.Package{{Name: "pkg"}},
		Channels: []declcfg.Channel{{Name: "ch", Package: "pkg", Entries: []declcfg.ChannelEntry{
			{Name: "b2", Skips: []string{"b1"}},
			{Name: "b1"},
		}}},
		Bundles: []declcfg.Bundle{
			{Name: "b1", Package: "pkg", Properties: propertiesForBundle("pkg", "1.0.0")},
			{Name: "b2", Package: "pkg", Properties: propertiesForBundle("pkg", "2.0.0")},
		},
	}, out)
	assert.Contains(t, logOutput.String(), `including bundle "b2" with version "2.0.0"`)
}

func propertiesForBundle(pkg, version string) []property.Property {
	return []property.Property{
		{Type: property.TypePackage, Value: []byte(fmt.Sprintf(`{"packageName": %q, "version": %q}`, pkg, version))},
	}
}

func loadDeclarativeConfig(t *testing.T) *declcfg.DeclarativeConfig {
	declCfg, err := declcfg.LoadFS(context.Background(), declCfgFS)
	if err != nil {
		t.Fatal(err)
	}
	return declCfg
}
