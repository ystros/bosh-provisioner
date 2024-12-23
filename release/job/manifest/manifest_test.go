package manifest_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/bosh-dep-forks/bosh-provisioner/release/job/manifest"
)

var _ = Describe("Manifest", func() {
	Describe("NewManifestFromBytes", func() {
		It("returns manifest with property definiton default that have string keys", func() {
			manifestBytes := []byte(`
properties:
  key:
    default:
      prop:
        nest-prop: instance-val
      props:
      - name: nest-prop
`)

			manifest, err := NewManifestFromBytes(manifestBytes)
			Expect(err).ToNot(HaveOccurred())

			Expect(manifest.Job.PropertyMappings).To(HaveLen(1))

			for _, propDef := range manifest.Job.PropertyMappings {
				// yaml unmarshals manifest to map[interface{}]interface{}
				// (encoding/json unmarshals manifest to map[string]interface{})
				Expect(propDef.Default).To(Equal(map[string]interface{}{
					"prop": map[string]interface{}{
						"nest-prop": "instance-val",
					},
					"props": []interface{}{
						map[string]interface{}{"name": "nest-prop"},
					},
				}))
			}
		})

		It("returns manifest with property definiton default that is an empty string", func() {
			manifestBytes := []byte(`
properties:
  key:
    default: ""
`)

			manifest, err := NewManifestFromBytes(manifestBytes)
			Expect(err).ToNot(HaveOccurred())

			Expect(manifest.Job.PropertyMappings).To(HaveLen(1))

			for _, propDef := range manifest.Job.PropertyMappings {
				Expect(propDef.Default).To(Equal(""))
			}
		})

		It("allows an empty properties definition", func() {
			manifestBytes := []byte(`
properties:`)

			manifest, err := NewManifestFromBytes(manifestBytes)
			Expect(err).ToNot(HaveOccurred())

			Expect(manifest.Job.PropertyMappings).To(HaveLen(0))
		})
	})
})
