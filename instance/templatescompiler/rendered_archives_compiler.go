package templatescompiler

import (
	"path/filepath"

	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"

	bpdep "github.com/bosh-dep-forks/bosh-provisioner/deployment"
	bperb "github.com/bosh-dep-forks/bosh-provisioner/instance/templatescompiler/erbrenderer"
	bpreljob "github.com/bosh-dep-forks/bosh-provisioner/release/job"
	bptar "github.com/bosh-dep-forks/bosh-provisioner/tar"
)

type RenderedArchivesCompiler struct {
	fs         boshsys.FileSystem
	runner     boshsys.CmdRunner
	compressor bptar.Compressor
	logger     boshlog.Logger
}

func NewRenderedArchivesCompiler(
	fs boshsys.FileSystem,
	runner boshsys.CmdRunner,
	compressor bptar.Compressor,
	logger boshlog.Logger,
) RenderedArchivesCompiler {
	return RenderedArchivesCompiler{
		fs:         fs,
		runner:     runner,
		compressor: compressor,
		logger:     logger,
	}
}

// Compile takes release jobs and instance and produces rendered templates archive.
// Rendered templates archive contains rendered job templates
// that can be unpacked by a GoAgent to populate a VM.
func (rac RenderedArchivesCompiler) Compile(relJobs []bpreljob.Job, instance bpdep.Instance) (string, error) {
	path, err := rac.fs.TempDir("instance-templatescompiler-RenderedArchivesCompiler")
	if err != nil {
		return "", bosherr.WrapError(err, "Creating compiled templates directory")
	}

	defer rac.fs.RemoveAll(path)

	for _, relJob := range relJobs {
		context := bperb.NewTemplateEvaluationContext(relJob, instance)

		renderer := bperb.NewERBRenderer(rac.fs, rac.runner, context, rac.logger)

		dstPath := filepath.Join(path, relJob.Name, "monit")

		err := renderer.Render(relJob.MonitTemplate.Path, dstPath)
		if err != nil {
			return "", bosherr.WrapError(err, "Rendering monit ERB")
		}

		for _, template := range relJob.Templates {
			dstPath := filepath.Join(path, relJob.Name, template.DstPathEnd)

			err := renderer.Render(template.Path, dstPath)
			if err != nil {
				return "", bosherr.WrapErrorf(err, "Rendering %s ERB", template.DstPathEnd)
			}
		}
	}

	renderedArchivePath, err := rac.compressor.Compress(path)
	if err != nil {
		return "", bosherr.WrapError(err, "Compressing templates")
	}

	return renderedArchivePath, nil
}

// CleanUp deletes previously produced rendered templates archive.
func (rac RenderedArchivesCompiler) CleanUp(path string) error {
	return rac.fs.RemoveAll(path)
}
