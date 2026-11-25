package v1alpha1

import (
	"bytes"
	"context"
	"slices"
	"testing"

	mmsemver "github.com/Masterminds/semver/v3"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestFilterChannelByVersionRange(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entries    []declcfg.ChannelEntry
		matches    []string
		want       []string
		connectors []string
		err        string
	}{
		{
			name: "stranded matching-only candidate needs connector",
			entries: []declcfg.ChannelEntry{
				{Name: "H", Replaces: "X", Skips: []string{"A"}},
				{Name: "X", Replaces: "B"}, {Name: "A", Replaces: "B"}, {Name: "B"},
			},
			matches: []string{"H", "A", "B"}, want: []string{"H", "X", "A", "B"}, connectors: []string{"X"},
		},
		{
			name: "nested skips cannot silently lose a match",
			entries: []declcfg.ChannelEntry{
				{Name: "H", Replaces: "B", Skips: []string{"A"}},
				{Name: "B"}, {Name: "A", Skips: []string{"C"}}, {Name: "C"},
			},
			matches: []string{"B", "C"}, err: "connector fallback cannot preserve all matching entries; missing: [C]",
		},
		{
			name: "fallback coverage is not enough without OLM validity",
			entries: []declcfg.ChannelEntry{
				{Name: "H", Replaces: "X", Skips: []string{"A"}},
				{Name: "X", Replaces: "B"}, {Name: "B"},
				{Name: "A", Skips: []string{"X", "C"}}, {Name: "C", Skips: []string{"B"}},
			},
			matches: []string{"H", "A", "B"}, err: "connector fallback produces an invalid channel: channel contains one or more stranded bundles: [B]",
		},
		{
			name: "missing nested matches are diagnosed in sorted order",
			entries: []declcfg.ChannelEntry{
				{Name: "H", Replaces: "B", Skips: []string{"A"}},
				{Name: "B"}, {Name: "A", Skips: []string{"D", "C"}}, {Name: "D"}, {Name: "C"},
			},
			matches: []string{"B", "D", "C"}, err: "connector fallback cannot preserve all matching entries; missing: [C D]",
		},
		{
			name: "necessary replaces connector",
			entries: []declcfg.ChannelEntry{
				{Name: "A", Replaces: "X"}, {Name: "X", Replaces: "B"}, {Name: "B"},
			},
			matches: []string{"A", "B"}, want: []string{"A", "X", "B"}, connectors: []string{"X"},
		},
		{
			name:    "unnecessary skip connector removed",
			entries: []declcfg.ChannelEntry{{Name: "H", Skips: []string{"B"}}, {Name: "B"}},
			matches: []string{"B"}, want: []string{"B"},
		},
		{
			name:    "dangling targets and skip range retained unchanged",
			entries: []declcfg.ChannelEntry{{Name: "A", Replaces: "missing", Skips: []string{"absent"}, SkipRange: ">=0.0.0 <1.0.0"}},
			matches: []string{"A"}, want: []string{"A"},
		},
		{
			name: "globally skipped replaces target stops OLM walk",
			entries: []declcfg.ChannelEntry{
				{Name: "H", Replaces: "A", Skips: []string{"A"}}, {Name: "A", Skips: []string{"B"}}, {Name: "B"},
			},
			matches: []string{"H", "A", "B"}, want: []string{"H", "A", "B"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := rangeTestCatalog(tc.entries, tc.matches)
			_, err := declcfg.ConvertToModel(*catalog)
			require.NoError(t, err, "original fixture must be accepted downstream")
			versions := map[string]*mmsemver.Version{}
			for _, b := range catalog.Bundles {
				versions[b.Name], err = getBundleVersion(b)
				require.NoError(t, err)
			}
			ch := catalog.Channels[0]
			originalEntries := slices.Clone(ch.Entries)
			if tc.name == "stranded matching-only candidate needs connector" {
				candidate := selectChannelEntries(ch, sets.New(tc.matches...))
				_, err := newChannel(candidate, nullLogger())
				require.NoError(t, err, "DAG validation alone misses the stranded B")
				require.ErrorContains(t, validateChannelReplacesChain(candidate), "stranded bundles: [B]")
				candidateCatalog := *catalog
				candidateCatalog.Channels = []declcfg.Channel{candidate}
				candidateCatalog.Bundles = slices.DeleteFunc(slices.Clone(catalog.Bundles), func(b declcfg.Bundle) bool {
					return !slices.Contains(tc.matches, b.Name)
				})
				_, err = declcfg.ConvertToModel(candidateCatalog)
				require.ErrorContains(t, err, "stranded bundles: B")
			}
			// Both equal bounds and ordinary ranges need identical connectors.
			for _, expression := range []string{">=1.0.0 <=1.0.0", "1.0.0", ">=1.0.0 <2.0.0"} {
				constraint, err := mmsemver.NewConstraint(expression)
				require.NoError(t, err)
				buffer := &bytes.Buffer{}
				logger := logrus.New()
				logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableQuote: true})
				logger.SetOutput(buffer)
				out, selected, err := filterChannelByVersionRange(ch, constraint, versions, logrus.NewEntry(logger))
				assert.Equal(t, originalEntries, ch.Entries, "candidate construction must not overwrite fallback input")
				if tc.err != "" {
					require.EqualError(t, err, tc.err)
					assert.Empty(t, buffer.String(), "failed fallback must not emit speculative warnings")
				} else {
					require.NoError(t, err)
					assert.ElementsMatch(t, tc.want, selected.UnsortedList())
					assert.Equal(t, selectChannelEntries(ch, sets.New(tc.want...)), out)
					require.NoError(t, validateRangeChannel(out))
					for _, connector := range tc.connectors {
						assert.Contains(t, buffer.String(), `including bundle "`+connector+`"`)
					}
					if len(tc.connectors) == 0 {
						assert.Empty(t, buffer.String())
					}
				}
				// Exercise the public path and actual downstream model conversion,
				// with complete FBC bundles rather than topology-only fixtures.
				f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "pkg", VersionRange: expression}}})
				result, err := f.FilterCatalog(context.Background(), catalog)
				if tc.err != "" {
					require.EqualError(t, err, `package "pkg" channel "stable" version range "`+expression+`": `+tc.err)
				} else {
					require.NoError(t, err)
					assert.ElementsMatch(t, tc.want, pinBundleNames(result))
					_, err = declcfg.ConvertToModel(*result)
					require.NoError(t, err)
				}
			}
		})
	}
}

func rangeTestCatalog(entries []declcfg.ChannelEntry, matches []string) *declcfg.DeclarativeConfig {
	catalog := &declcfg.DeclarativeConfig{
		Packages: []declcfg.Package{{Schema: declcfg.SchemaPackage, Name: "pkg", DefaultChannel: "stable"}},
		Channels: []declcfg.Channel{{Schema: declcfg.SchemaChannel, Name: "stable", Package: "pkg", Entries: entries}},
	}
	for _, entry := range entries {
		version := "2.0.0+" + entry.Name
		if slices.Contains(matches, entry.Name) {
			version = "1.0.0+" + entry.Name
		}
		catalog.Bundles = append(catalog.Bundles, declcfg.Bundle{
			Schema: declcfg.SchemaBundle, Name: entry.Name, Package: "pkg",
			Image: "quay.io/example/bundle:" + entry.Name, Properties: propertiesForBundle("pkg", version),
		})
	}
	return catalog
}

func TestVersionRangeRejectsInvalidOriginal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []declcfg.ChannelEntry
		err     string
	}{
		{"duplicate", []declcfg.ChannelEntry{{Name: "A"}, {Name: "A"}}, "duplicate channel entry"},
		{"multiple heads", []declcfg.ChannelEntry{{Name: "A"}, {Name: "X"}}, "multiple channel heads"},
		{"skip cycle", []declcfg.ChannelEntry{{Name: "A", Skips: []string{"X"}}, {Name: "X", Skips: []string{"X"}}}, "skips itself"},
		{"nested skip cycle", []declcfg.ChannelEntry{{Name: "A", Skips: []string{"X"}}, {Name: "X", Skips: []string{"Y"}}, {Name: "Y", Skips: []string{"X"}}}, "detected a cycle"},
		{"stranded", []declcfg.ChannelEntry{{Name: "A", Replaces: "missing", Skips: []string{"X"}}, {Name: "X", Replaces: "B"}, {Name: "B"}}, "stranded bundles: [B]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraint, err := mmsemver.NewConstraint("1.0.0")
			require.NoError(t, err)
			ch := declcfg.Channel{Name: "stable", Package: "pkg", Entries: tc.entries}
			_, _, err = filterChannelByVersionRange(ch, constraint, map[string]*mmsemver.Version{"A": mmsemver.MustParse("1.0.0")}, nullLogger())
			require.ErrorContains(t, err, "invalid original channel")
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestVersionRangeExplicitEntriesOnly(t *testing.T) {
	constraint, err := mmsemver.NewConstraint("1.0.0")
	require.NoError(t, err)
	ch := declcfg.Channel{Name: "stable", Entries: []declcfg.ChannelEntry{{Name: "A", Replaces: "X", Skips: []string{"missing"}}, {Name: "X", Replaces: "B"}, {Name: "B"}}}
	versions := map[string]*mmsemver.Version{
		"A": mmsemver.MustParse("1.0.0"), "B": mmsemver.MustParse("1.0.0+rebuild"),
		"missing": mmsemver.MustParse("1.0.0"), "other-channel": mmsemver.MustParse("1.0.0"),
		// X is unversioned: not a strict match, but a necessary connector.
	}
	buffer := &bytes.Buffer{}
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableQuote: true})
	logger.SetOutput(buffer)
	out, selected, err := filterChannelByVersionRange(ch, constraint, versions, logrus.NewEntry(logger))
	require.NoError(t, err)
	assert.Equal(t, ch, out)
	assert.ElementsMatch(t, []string{"A", "X", "B"}, selected.UnsortedList())
	assert.Contains(t, buffer.String(), `including bundle "X": it is unversioned`)
	assert.NotContains(t, buffer.String(), `including bundle "missing"`)

	// Neither nil versions nor matching placeholders create strict matches.
	ch.Entries = []declcfg.ChannelEntry{{Name: "unversioned", Replaces: "missing"}}
	out, selected, err = filterChannelByVersionRange(ch, constraint, versions, nullLogger())
	require.NoError(t, err)
	assert.Empty(t, out.Entries)
	assert.Empty(t, selected)
}

func TestVersionRangeConstraintSemantics(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       []string
	}{
		{">=7.4.6-6.1 <=7.4.6-6.1", []string{"redis-pin"}},
		{"7.4.6-6.1", []string{"redis-pin"}},
		{">=1.2.3-alpha.1+first <=1.2.3-alpha.1+last", []string{"pre", "rebuild"}},
		{"<=1.2.3-alpha.1, >=1.2.3-alpha.1", []string{"pre", "rebuild"}},
		{"1.2.3", []string{"release"}},
		{">=1.2.3 <1.2.4", []string{"release"}},
		{"1.2.3-alpha.1 || 1.2.3", []string{"pre", "rebuild", "release"}},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			// A replaces chain with one skipped neighbor makes each expected
			// set valid without connectors, including the disjunction.
			names := []string{"pre-earlier", "pre", "rebuild", "pre-later", "release", "next-pre", "redis-pin", "redis-higher"}
			versions := []string{"1.2.3-alpha.0", "1.2.3-alpha.1", "1.2.3-alpha.1+build", "1.2.3-alpha.2", "1.2.3", "1.2.4-alpha.1", "7.4.6-6.1", "7.8.6-1.1"}
			catalog := rangeTestCatalog(nil, nil)
			for i, name := range names {
				entry := declcfg.ChannelEntry{Name: name}
				if i > 0 {
					entry.Replaces = names[i-1]
				}
				if name == "release" {
					entry.Replaces = "rebuild"
					entry.Skips = []string{"pre-later"}
				}
				catalog.Channels[0].Entries = append(catalog.Channels[0].Entries, entry)
				catalog.Bundles = append(catalog.Bundles, declcfg.Bundle{Schema: declcfg.SchemaBundle, Name: name, Package: "pkg", Image: "quay.io/example/" + name + ":latest", Properties: propertiesForBundle("pkg", versions[i])})
			}
			f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "pkg", VersionRange: tc.expression}}})
			out, err := f.FilterCatalog(context.Background(), catalog)
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, pinBundleNames(out))
			_, err = declcfg.ConvertToModel(*out)
			require.NoError(t, err)
		})
	}
}

func TestVersionRangeChannelScopeAndEmptyChannels(t *testing.T) {
	makeCatalog := func() *declcfg.DeclarativeConfig {
		catalog := versionPinCatalog()
		// An equal-version rebuild outside the selected channel must never win
		// a package-wide lookup, nor be promoted through a dangling reference.
		catalog.Channels[0].Entries[0].Replaces = "excluded"
		catalog.Bundles = append(catalog.Bundles, declcfg.Bundle{Schema: declcfg.SchemaBundle, Name: "excluded", Package: "web-terminal", Image: "quay.io/example/excluded:latest", Properties: propertiesForBundle("web-terminal", "1.12.1+excluded")})
		catalog.Channels = append(catalog.Channels, declcfg.Channel{Schema: declcfg.SchemaChannel, Name: "other", Package: "web-terminal", Entries: []declcfg.ChannelEntry{{Name: "excluded", Replaces: "web-terminal.v1.12.1"}, {Name: "web-terminal.v1.12.1"}}})
		return catalog
	}
	t.Run("excluded channel rebuild", func(t *testing.T) {
		f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "web-terminal", Channels: []Channel{{Name: "fast", VersionRange: "1.12.1"}}}}})
		out, err := f.FilterCatalog(context.Background(), makeCatalog())
		require.NoError(t, err)
		assert.Len(t, out.Bundles, 4)
		assert.NotContains(t, pinBundleNames(out), "excluded")
		_, err = declcfg.ConvertToModel(*out)
		require.NoError(t, err)
	})
	t.Run("shared bundles and per-channel selection", func(t *testing.T) {
		f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "web-terminal", Channels: []Channel{{Name: "fast", VersionRange: "1.12.1"}, {Name: "other", VersionRange: "1.12.1"}}}}})
		out, err := f.FilterCatalog(context.Background(), makeCatalog())
		require.NoError(t, err)
		assert.Len(t, out.Bundles, 5)
		assert.Len(t, out.Channels[0].Entries, 4)
		assert.Len(t, out.Channels[1].Entries, 2)
		_, err = declcfg.ConvertToModel(*out)
		require.NoError(t, err)
	})
	t.Run("nondefault no matches removed", func(t *testing.T) {
		f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "web-terminal", Channels: []Channel{{Name: "fast", VersionRange: "1.12.1"}, {Name: "other", VersionRange: "9.0.0"}}}}})
		out, err := f.FilterCatalog(context.Background(), makeCatalog())
		require.NoError(t, err)
		require.Len(t, out.Channels, 1)
		assert.Equal(t, "fast", out.Channels[0].Name)
		assert.Len(t, out.Bundles, 4)
		_, err = declcfg.ConvertToModel(*out)
		require.NoError(t, err)
	})
	t.Run("default no matches errors", func(t *testing.T) {
		f := NewMirrorFilter(FilterConfiguration{Packages: []Package{{Name: "web-terminal", VersionRange: "9.0.0"}}})
		_, err := f.FilterCatalog(context.Background(), makeCatalog())
		require.EqualError(t, err, `package "web-terminal" channel "fast" has version range "9.0.0" that results in an empty channel`)
	})
}
