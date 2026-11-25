package v1alpha1

import (
	"errors"
	"fmt"
	"sort"

	mmsemver "github.com/Masterminds/semver/v3"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/model"
)

type channelEntry struct {
	Name    string
	Version *mmsemver.Version

	Replaces *channelEntry
	Skips    sets.Set[*channelEntry]
}

type channel struct {
	head *channelEntry
	log  *logrus.Entry
}

// filterChannelByVersionRange retains every matching explicit entry, including
// semver-equivalent rebuilds. Only retain out-of-range connectors when the
// matching-only graph is invalid; never let the legacy traversal lose a match.
func filterChannelByVersionRange(ch declcfg.Channel, constraint *mmsemver.Constraints, versions map[string]*mmsemver.Version, log *logrus.Entry) (declcfg.Channel, sets.Set[string], error) {
	// Validate before filtering so selection cannot hide invalid input. Suppress
	// speculative warnings until a fallback has passed all checks.
	original, err := newChannel(ch, nullLogger())
	if err != nil {
		return declcfg.Channel{}, nil, fmt.Errorf("invalid original channel: %w", err)
	}
	if err := validateChannelReplacesChain(ch); err != nil {
		return declcfg.Channel{}, nil, fmt.Errorf("invalid original channel: %w", err)
	}
	matches := sets.New[string]()
	channelVersions := make(map[string]*mmsemver.Version, len(ch.Entries))
	for _, entry := range ch.Entries {
		v := versions[entry.Name]
		channelVersions[entry.Name] = v
		if v != nil && constraint.Check(v) {
			matches.Insert(entry.Name)
		}
	}
	candidate := selectChannelEntries(ch, matches)
	if len(matches) == 0 {
		return candidate, matches, nil
	}
	if err := validateRangeChannel(candidate); err == nil {
		return candidate, matches, nil
	}
	selected := original.filterByVersionRange(constraint, channelVersions)
	// Placeholders for dangling references are legal graph nodes, not entries.
	for name := range selected {
		if _, explicit := channelVersions[name]; !explicit {
			selected.Delete(name)
		}
	}
	if missing := sets.List(matches.Difference(selected)); len(missing) > 0 {
		return declcfg.Channel{}, nil, fmt.Errorf("connector fallback cannot preserve all matching entries; missing: %v", missing)
	}
	candidate = selectChannelEntries(ch, selected)
	if err := validateRangeChannel(candidate); err != nil {
		return declcfg.Channel{}, nil, fmt.Errorf("connector fallback produces an invalid channel: %w", err)
	}
	for _, entry := range candidate.Entries {
		if matches.Has(entry.Name) {
			continue
		}
		if v := channelVersions[entry.Name]; v == nil {
			log.Warnf("including bundle %q: it is unversioned but is required to ensure inclusion of all bundles in the range", entry.Name)
		} else {
			log.Warnf("including bundle %q with version %q: it falls outside the specified range of %q but is required to ensure inclusion of all bundles in the range", entry.Name, v, constraint)
		}
	}
	return candidate, selected, nil
}

func selectChannelEntries(ch declcfg.Channel, names sets.Set[string]) declcfg.Channel {
	entries := make([]declcfg.ChannelEntry, 0, len(names))
	for _, entry := range ch.Entries {
		if names.Has(entry.Name) {
			entries = append(entries, entry)
		}
	}
	ch.Entries = entries
	return ch
}

func validateRangeChannel(ch declcfg.Channel) error {
	if _, err := newChannel(ch, nullLogger()); err != nil {
		return err
	}
	return validateChannelReplacesChain(ch)
}

// Mirror model.Channel.validateReplacesChain's topology checks, reusing its
// Head implementation. The dependency's chain validator is private and its
// public Validate also requires bundle images/properties unrelated to topology.
// Skips are global, missing targets are legal, and SkipRange is not an edge.
// Keep newChannel's stricter skip-cycle checks in addition to these checks.
func validateChannelReplacesChain(ch declcfg.Channel) error {
	c := model.Channel{Bundles: make(map[string]*model.Bundle, len(ch.Entries))}
	skipped := sets.New[string]()
	all := sets.New[string]()
	for _, entry := range ch.Entries {
		c.Bundles[entry.Name] = &model.Bundle{Name: entry.Name, Replaces: entry.Replaces, Skips: entry.Skips}
		all.Insert(entry.Name)
		skipped.Insert(entry.Skips...)
	}
	head, err := c.Head()
	if err != nil {
		return err
	}
	chain := sets.New[string](head.Name)
	for cur := head; cur != nil; cur = c.Bundles[cur.Replaces] {
		if skipped.Has(cur.Replaces) {
			break
		}
		if chain.Has(cur.Replaces) {
			return fmt.Errorf("detected cycle in replaces chain at %q", cur.Replaces)
		}
		chain.Insert(cur.Replaces)
	}
	if stranded := sets.List(all.Difference(chain).Difference(skipped)); len(stranded) > 0 {
		return fmt.Errorf("channel contains one or more stranded bundles: %v", stranded)
	}
	return nil
}

func newChannel(ch declcfg.Channel, log *logrus.Entry) (*channel, error) {
	if len(ch.Entries) == 0 {
		return nil, errors.New("channel has no entries")
	}

	entrySet := sets.NewString()
	entryMap := make(map[string]*channelEntry, len(ch.Entries))
	incoming := make(map[string]sets.Set[*channelEntry], len(ch.Entries))

	var errs []error
	for _, e := range ch.Entries {
		ce, ok := entryMap[e.Name]
		if !ok {
			// Create a new channel entry and add it to the map.
			ce = &channelEntry{
				Name: e.Name,
			}
			entryMap[e.Name] = ce
		}

		// Check for duplicates, and error if a dup is found.
		if entrySet.Has(ce.Name) {
			errs = append(errs, fmt.Errorf("duplicate channel entry %q", e.Name))
			continue
		}
		entrySet.Insert(e.Name)

		// If this bundle replaces another bundle, increment the incoming count
		// for the other bundle,  find (or create) the other bundle's channel
		// entry, and then assign it to this bundle's replaces value.
		if e.Replaces != "" {
			if e.Name == e.Replaces {
				errs = append(errs, fmt.Errorf("invalid channel entry %q: replaces itself", e.Name))
			}
			replaces, ok := entryMap[e.Replaces]
			if !ok {
				replaces = &channelEntry{
					Name: e.Replaces,
				}
				entryMap[e.Replaces] = replaces
			}
			if _, ok := incoming[replaces.Name]; !ok {
				incoming[replaces.Name] = sets.New[*channelEntry]()
			}
			incoming[replaces.Name].Insert(ce)
			ce.Replaces = replaces
		}

		// Get (or create) skips entries for all of this bundle's skips,
		// increment their incoming values, and then set this bundle's skips
		// list to the slice of skips entries we built.
		skips := sets.New[*channelEntry]()
		for _, skipName := range e.Skips {
			if e.Name == skipName {
				errs = append(errs, fmt.Errorf("invalid channel entry %q: skips itself", e.Name))
			}
			skip, ok := entryMap[skipName]
			if !ok {
				skip = &channelEntry{
					Name: skipName,
				}
				entryMap[skipName] = skip
			}
			if _, ok := incoming[skip.Name]; !ok {
				incoming[skip.Name] = sets.New[*channelEntry]()
			}
			incoming[skip.Name].Insert(ce)
			skips.Insert(skip)
		}
		if skips.Len() > 0 {
			ce.Skips = skips
		}
	}

	// Find all of the channel heads (the bundles that have no incoming edges)
	var heads []*channelEntry
	for _, e := range ch.Entries {
		if incoming[e.Name].Len() == 0 {
			heads = append(heads, entryMap[e.Name])
		}
	}
	if len(heads) == 0 {
		errs = append(errs, errors.New("no channel heads found"))
	} else if len(heads) > 1 {
		headNames := make([]string, 0, len(heads))
		for _, h := range heads {
			headNames = append(headNames, h.Name)
		}
		sort.Strings(headNames)
		errs = append(errs, fmt.Errorf("multiple channel heads found: %v", headNames))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	// Topological sort the channel. If we can successfully perform a topological
	// sort, then we know there are no cycles.
	queue := []*channelEntry{heads[0]}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.Replaces != nil {
			// If a channelEntry is both in the `Replaces` and `Skips` list of
			// `cur`, it will be removed from the `incoming` map in the
			// one of the 2 checks. However because Golang maps return 0-value
			// for non-existent keys, we need to explicitely check the entry
			// has not been already removed before we add it to the queue. If
			// we don't, the queue will end up with many duplicates of the same
			// entry. For channels with many entries and long skip/replaces
			// chains, this can even cause OOM.
			if _, ok := incoming[cur.Replaces.Name]; ok {
				incoming[cur.Replaces.Name].Delete(cur)
				if incoming[cur.Replaces.Name].Len() == 0 {
					delete(incoming, cur.Replaces.Name)
					queue = append(queue, cur.Replaces)
				}
			}
		}
		for _, n := range cur.Skips.UnsortedList() {
			if _, ok := incoming[n.Name]; !ok {
				continue
			}
			incoming[n.Name].Delete(cur)
			if incoming[n.Name].Len() == 0 {
				delete(incoming, n.Name)
				queue = append(queue, n)
			}
		}
	}

	// If we exhaust our queue and there are still incoming edges left
	// untraversed, it means we have a cycle.
	if len(incoming) > 0 {
		return nil, errors.New("detected a cycle in the upgrade graph of the channel")
	}

	return &channel{
		head: heads[0],
		log:  log,
	}, nil
}

// filterByVersionRange filters out bundles from the channel that do not fall within the version range.
//
// This is a bit tricky because we don't want to create additional channel heads, which might mean including extra
// bundles that fall outside the version range. If this happens, we will emit a warning for each bundle that falls
// outside the range.
//
// For each existing channel head, we need to find a new head and new tail bundle. We will count the number of bundles in
// the version range that are at or below each bundle in the replaces chain. In order to get the minimal set, we will
// keep track of the specific bundles that we have seen and only count them once.
//   - The new head will be the bundle with the most bundles at or below it. If multiple bundles have the same number
//     of version range matches at or below them, we will use the bundle lowest in the replaces chain.
//   - The tail will be the first bundle in the replaces chain whose version range match count is 0. The tail is not
//     included in the new chain.
func (c *channel) filterByVersionRange(versionRange *mmsemver.Constraints, versionMap map[string]*mmsemver.Version) sets.Set[string] {
	keepEntries := sets.New[string]()

	for cur := c.head; cur != nil; cur = cur.Replaces {
		cur.Version = versionMap[cur.Name]
		for _, skip := range cur.Skips.UnsortedList() {
			skip.Version = versionMap[skip.Name]
		}
	}

	seen := sets.New[string]()
	counts := map[string]int{}
	countUniqueTailBundlesInRange(c.head, versionRange, seen, counts)
	maxCount := -1

	// Find:
	// - head (lowest node on replaces chain that has the maximum
	//   count of unvisited tail nodes in the version range)
	// - tail (highest node on the replaces chain that has 0 unvisited tail
	//   nodes in the version range)
	var head, tail *channelEntry
	for cur := c.head; cur != nil; cur = cur.Replaces {
		count := counts[cur.Name]
		if count >= maxCount {
			head = cur
			maxCount = count
		}
		if count == 0 {
			tail = cur
			break
		}
	}

	// We how have head and tail, let's traverse head to tail and build a list of bundles to keep,
	// emitting a warning if anything in the replaces chain is not in the version range.
	for cur := head; cur != tail; cur = cur.Replaces {
		if cur.Version == nil {
			c.log.Warnf("including bundle %q: it is unversioned but is required to ensure inclusion of all bundles in the range", cur.Name)
		} else if !versionRange.Check(cur.Version) {
			c.log.Warnf("including bundle %q with version %q: it falls outside the specified range of %q but is required to ensure inclusion of all bundles in the range", cur.Name, cur.Version, versionRange)
		}
		keepEntries.Insert(cur.Name)
		for _, skip := range cur.Skips.UnsortedList() {
			if skip.Version != nil && versionRange.Check(skip.Version) {
				keepEntries.Insert(skip.Name)
			}
		}
	}
	return keepEntries
}

// countUniqueTailBundlesInRange counts the number of bundles in the replaces chain of b that are in the version range
// that are unique to b, where "in the replaces chain" is defined as "b or any bundle that b skips, or any bundle in
// the replaces chain of b's replaces bundle"
func countUniqueTailBundlesInRange(entry *channelEntry, versionConstraints *mmsemver.Constraints, seen sets.Set[string], counts map[string]int) {
	replaces := entry.Replaces
	count := 0
	if replaces != nil {
		countUniqueTailBundlesInRange(replaces, versionConstraints, seen, counts)
		count += counts[replaces.Name]
	}

	if !seen.Has(entry.Name) && entry.Version != nil && versionConstraints.Check(entry.Version) {
		seen.Insert(entry.Name)
		count++
	}

	for _, skip := range entry.Skips.UnsortedList() {
		if !seen.Has(skip.Name) && skip.Version != nil && versionConstraints.Check(skip.Version) {
			seen.Insert(skip.Name)
			count++
		}
	}

	counts[entry.Name] = count
}
