package v1alpha1

import (
	"errors"
	"fmt"
	"io"

	"github.com/Masterminds/semver/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// FilterConfigurationV1 is a configuration for filtering a set of packages and channels from a catalog.
// It supports selecting specific packages and specific channels and/or versions within those packages.
// The configuration is intended to be used with the `opm render` command to generate a filtered catalog.
type FilterConfiguration struct {
	metav1.TypeMeta `json:",inline"`

	// Packages is a list of packages to include in the filtered catalog.
	Packages []Package `json:"packages"`
}

type Package struct {
	// Name is the name of the package to filter.
	Name string `json:"name"`

	// DefaultChannel is the new default channel to use for the package.
	// If not set, the default channel will be the same as the original default channel.
	// If the original default channel is not in the filtered catalog, this field must be set.
	DefaultChannel string `json:"defaultChannel,omitempty"`

	// VersionRange is a semver range to filter the versions of the channel.
	// If not set, all versions will be included.
	VersionRange string `json:"versionRange,omitempty"`

	// Channels is a list of channels to include in the filtered catalog.
	// If not set, all channels will be included.
	Channels []Channel `json:"channels,omitempty"`

	SelectedBundles []SelectedBundle `json:"bundles,omitempty"`
}

type Channel struct {
	// Name is the name of the channel to include in the filtered catalog.
	Name string `json:"name"`

	// VersionRange is a semver range to filter the versions of the channel.
	// If not set, all versions will be included.
	VersionRange string `json:"versionRange,omitempty"`
}

type SelectedBundle struct {
	Name string `json:"name" yaml:"name"`
}

func LoadFilterConfiguration(r io.Reader) (*FilterConfiguration, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	cfg := &FilterConfiguration{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (f *FilterConfiguration) Validate() error {
	var errs []error
	if f.APIVersion != FilterAPIVersion {
		errs = append(errs, fmt.Errorf("unexpected API version %q", f.APIVersion))
	}
	if f.Kind != FilterKind {
		errs = append(errs, fmt.Errorf("unexpected kind %q", f.Kind))
	}
	for i, pkg := range f.Packages {
		if pkg.Name == "" {
			errs = append(errs, fmt.Errorf("package %q at index [%d] is invalid: name must be specified", pkg.Name, i))
		}
		if len(pkg.SelectedBundles) > 0 && (len(pkg.Channels) > 0 || pkg.VersionRange != "") {
			errs = append(errs, fmt.Errorf("package %q at index [%d] is invalid: mixing both filtering by bundles and filtering by channels or versionRange is not allowed", pkg.Name, i))
		}
		if pkg.VersionRange != "" {
			_, err := semver.NewConstraint(pkg.VersionRange)
			if err != nil {
				errs = append(errs, fmt.Errorf("package %q at index [%d] is invalid: versionRange is not in valid semantic versionning format: %v", pkg.Name, i, err))
			}
		}
		for j, channel := range pkg.Channels {
			if channel.Name == "" {
				errs = append(errs, fmt.Errorf("package %q at index [%d] is invalid: channel %q at index [%d] is invalid: name must be specified", pkg.Name, i, channel.Name, j))
			}
			if channel.VersionRange != "" && pkg.VersionRange != "" {
				errs = append(errs, fmt.Errorf("package %q at index [%d] is invalid: package specifies a VersionRange, while channel %q at index [%d] equally specifies one: package.VersionRange and channel.VersionRange are exclusive", pkg.Name, i, channel.Name, j))
			}
			if channel.VersionRange != "" {
				_, err := semver.NewConstraint(channel.VersionRange)
				if err != nil {
					errs = append(errs, fmt.Errorf("package %q at index [%d] is invalid: channel %q at index [%d] is invalid: versionRange is not in valid semantic versionning format: %v", pkg.Name, i, channel.Name, j, err))
				}
			}
		}
	}
	return errors.Join(errs...)
}
