This repo is a filtering API for [operator catalogs](https://olm.operatorframework.io/docs/reference/file-based-catalogs/).

Three implementations exist: 
* [Filtering by specifying a list of packages](https://github.com/sherine-k/catalog-filter/blob/main/pkg/filter/filter.go)
* Filtering through a configuration file
  * According to [oc-mirror](https://github.com/openshift/oc-mirror) [requirements](https://github.com/sherine-k/catalog-filter/blob/main/pkg/filter/mirror-config/v1alpha1/mirror-filter.go)
  * [Alpha implementation](https://github.com/sherine-k/catalog-filter/blob/main/pkg/filter/config/v1alpha1/filter.go) 

The filtering by configuration for oc-mirror is the only implementation that will be maintained for the moment. 

A valid configuration file for this implementation would look like: 
```yaml
apiVersion: olm.operatorframework.io/filter/mirror/v1alpha1
kind: FilterConfiguration
packages:
  - name: "foo"
  - name: "bar"
    defaultChannel: "bar-channel1"
    channels:
      - name: "bar-channel1"
        versionRange: ">=1.0.0 <2.0.0"
      - name: "bar-channel2"
        versionRange: ">=2.0.0 <3.0.0"
```

The above filtering would return a file based catalog json (FBC) that contains:
* only packages foo and bar from the original catalog provided
* within package foo, 1 channel will remain : foo's default channel
* in foo's default channel, 1 channel entry will remain: the head bundle of the channel
* within package foo, 1 bundle will remain: corresponding to the head of foo's default channel
* within package bar, only 2 channels will remain: bar-channel1 and bar-channel2
* within package bar, bar-channel1 will be set as the default channel
* in bar-channel1, all entries between 1.0.0 and 2.0.0 will remain, by following `skip`and `replace` chain. More entries may remain in order to ensure the channel has a single head, and doesn't have cycles 
* in bar-channel2, all entries between 2.0.0 and 3.0.0 will remain, by following `skip`and `replace` chain. More entries may remain in order to ensure the channel has a single head, and doesn't have cycles 