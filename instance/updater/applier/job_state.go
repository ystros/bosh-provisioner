package applier

import (
	boshas "github.com/cloudfoundry/bosh-agent/agent/applier/applyspec"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"

	bpdep "github.com/bosh-dep-forks/bosh-provisioner/deployment"
	bptplcomp "github.com/bosh-dep-forks/bosh-provisioner/instance/templatescompiler"
	bppkgscomp "github.com/bosh-dep-forks/bosh-provisioner/packagescompiler"
	boshcrypto "github.com/cloudfoundry/bosh-utils/crypto"
)

// JobState represents state for a VM
// that should be running 1+ job templates.
type JobState struct {
	depJob   bpdep.Job
	instance bpdep.Instance

	templatesCompiler bptplcomp.TemplatesCompiler
	packagesCompiler  bppkgscomp.PackagesCompiler

	emptyState EmptyState
}

func NewJobState(
	depJob bpdep.Job,
	instance bpdep.Instance,
	templatesCompiler bptplcomp.TemplatesCompiler,
	packagesCompiler bppkgscomp.PackagesCompiler,
) JobState {
	return JobState{
		depJob:   depJob,
		instance: instance,

		templatesCompiler: templatesCompiler,
		packagesCompiler:  packagesCompiler,

		emptyState: NewEmptyState(instance),
	}
}

func (s JobState) AsApplySpec() (boshas.V1ApplySpec, error) {
	var err error

	spec := s.emptyState.AsApplySpec()

	// JobTemplateSpecs list templates names; however,
	// actual template content would come from RenderedTemplatesArchiveSpec
	spec.JobSpec.JobTemplateSpecs = s.buildJobTemplateSpecs()

	// Package dependencies for all job templates
	spec.PackageSpecs, err = s.buildPackageSpecs()
	if err != nil {
		return spec, err
	}

	// Provides content for JobTemplateSpecs
	spec.RenderedTemplatesArchiveSpec, err = s.buildRenderedTemplatesArchive()
	if err != nil {
		return spec, err
	}

	return spec, nil
}

func (s JobState) buildJobTemplateSpecs() []boshas.JobTemplateSpec {
	var specs []boshas.JobTemplateSpec

	for _, template := range s.depJob.Templates {
		spec := boshas.JobTemplateSpec{
			Name:    template.Name,
			Version: "fake-job-template-version", // todo
		}

		specs = append(specs, spec)
	}

	return specs
}

func (s JobState) buildPackageSpecs() (map[string]boshas.PackageSpec, error) {
	specs := map[string]boshas.PackageSpec{}

	for _, template := range s.depJob.Templates {
		pkgs, err := s.templatesCompiler.FindPackages(template)
		if err != nil {
			return specs, bosherr.WrapErrorf(err, "Finding packages for template %s", template.Name)
		}

		for _, pkg := range pkgs {
			rec, err := s.packagesCompiler.FindCompiledPackage(pkg)
			if err != nil {
				return specs, bosherr.WrapErrorf(err, "Finding compiled package %s", pkg.Name)
			}

			specs[pkg.Name] = boshas.PackageSpec{
				Name:    pkg.Name,
				Version: pkg.Version,

				Sha1:        rec.SHA1,
				BlobstoreID: rec.BlobID,
			}
		}
	}

	return specs, nil
}

func (s JobState) buildRenderedTemplatesArchive() (*boshas.RenderedTemplatesArchiveSpec, error) {
	var archive boshas.RenderedTemplatesArchiveSpec

	rec, err := s.templatesCompiler.FindRenderedArchive(s.depJob, s.instance)
	if err != nil {
		return nil, bosherr.WrapErrorf(
			err, "Finding rendered archive %s", s.depJob.Name)
	}

	digest := boshcrypto.MustNewMultipleDigest(
		boshcrypto.NewDigest(boshcrypto.DigestAlgorithmSHA1, rec.SHA1),
	)
	// todo uppercase Sha1
	archive.Sha1 = &digest
	archive.BlobstoreID = rec.BlobID

	return &archive, nil
}

/*
// Example apply spec
{
  "job": {
    "name": "router",
    "template": "router template",
    "version": "1.0",
    "sha1": "router sha1",
    "blobstore_id": "router-blob-id-1",
    "templates": [{
      "name": "template 1",
      "version": "0.1",
      "sha1": "template 1 sha1",
      "blobstore_id": "template-blob-id-1"
    }]
  },

  "index": 1,

  "packages": {
    "package 1": {
      "name": "package 1",
      "version": "0.1",
      "sha1": "package 1 sha1",
      "blobstore_id": "package-blob-id-1"
    }
  },

  "networks": {
    "manual-net": {
      "ip": "xx.xx.xx.xx",
      "gateway": "xx.xx.xx.xx",
      "netmask": "xx.xx.xx.xx",
      "dns": ["xx.xx.xx.xx"],
      "default": ["dns", "gateway"],
      "cloud_properties": {"subnet": "subnet-xxxxxx"},
      "dns_record_name": "job-index.job-name.manual-net.deployment-name.bosh"
    },
    "vip-net": {
      "type": "vip",
      "ip": "xx.xx.xx.xx",
      "cloud_properties": {"security_groups": ["bosh"]},
      "dns_record_name": "job-index.job-name.vip-net.deployment-name.bosh"
    }
  },

  "rendered_templates_archive": {
    "sha1": "archive sha 1",
    "blobstore_id": "archive-blob-id-1"
  }
}
*/
