package vagrant

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"

	bpagclient "github.com/bosh-dep-forks/bosh-provisioner/agent/client"
	bpdep "github.com/bosh-dep-forks/bosh-provisioner/deployment"
	bpeventlog "github.com/bosh-dep-forks/bosh-provisioner/eventlog"
	bpvm "github.com/bosh-dep-forks/bosh-provisioner/vm"
)

const (
	agentProvisionerLogTag        = "AgentProvisioner"
	agentProvisionerRunitName     = "agent"
	agentProvisionerRunitStopTime = 10 * time.Second
)

// AgentProvisioner places BOSH Agent and Monit onto machine
// installing needed dependencies beforehand.
type AgentProvisioner struct {
	fs           boshsys.FileSystem
	cmds         SimpleCmds
	assetManager AssetManager

	runitProvisioner RunitProvisioner
	monitProvisioner MonitProvisioner

	blobstoreConfig        map[string]interface{}
	agentProvisionerConfig bpvm.AgentProvisionerConfig

	eventLog bpeventlog.Log
	logger   boshlog.Logger
}

func NewAgentProvisioner(
	fs boshsys.FileSystem,
	cmds SimpleCmds,
	assetManager AssetManager,
	runitProvisioner RunitProvisioner,
	monitProvisioner MonitProvisioner,
	blobstoreConfig map[string]interface{},
	agentProvisionerConfig bpvm.AgentProvisionerConfig,
	eventLog bpeventlog.Log,
	logger boshlog.Logger,
) AgentProvisioner {
	return AgentProvisioner{
		fs:           fs,
		cmds:         cmds,
		assetManager: assetManager,

		runitProvisioner: runitProvisioner,
		monitProvisioner: monitProvisioner,

		blobstoreConfig:        blobstoreConfig,
		agentProvisionerConfig: agentProvisionerConfig,

		eventLog: eventLog,
		logger:   logger,
	}
}

func (p AgentProvisioner) Provision() error {
	stage := p.eventLog.BeginStage("Updating BOSH agent", 4)

	// Deprovision possibly provisioned agent
	// to avoid replacing running binaries on fs
	err := p.runitProvisioner.Deprovision(
		agentProvisionerRunitName,
		agentProvisionerRunitStopTime,
	)
	if err != nil {
		return bosherr.WrapError(err, "Deprovisioning agent with runit")
	}

	err = p.monitProvisioner.Deprovision()
	if err != nil {
		return bosherr.WrapError(err, "Deprovisioning monit")
	}

	task := stage.BeginTask("Placing binaries")

	err = task.End(p.placeBinaries())
	if err != nil {
		return bosherr.WrapError(err, "Placing agent binaries")
	}

	task = stage.BeginTask("Placing configuration files")

	err = task.End(p.placeConfFiles())
	if err != nil {
		return bosherr.WrapError(err, "Configuring settings")
	}

	task = stage.BeginTask("Registering monit service")

	err = task.End(p.monitProvisioner.Provision())
	if err != nil {
		return bosherr.WrapError(err, "Provisioning monit")
	}

	task = stage.BeginTask("Registering agent service")

	err = task.End(p.runitProvisioner.Provision(
		agentProvisionerRunitName,
		agentProvisionerRunitStopTime,
	))
	if err != nil {
		return bosherr.WrapError(err, "Provisioning agent with runit")
	}

	return nil
}

func (p AgentProvisioner) Configure(instance bpdep.Instance) (bpagclient.Client, error) {
	stage := p.eventLog.BeginStage("Configuring BOSH agent", 1)

	err := p.placeInfSettings(instance)
	if err != nil {
		return nil, bosherr.WrapError(err, "Placing infrastructure settings")
	}

	task := stage.BeginTask("Configuring infrastructure settings")

	agentClient, err := p.buildAgentClient()
	if task.End(err) != nil {
		return nil, bosherr.WrapError(err, "Building agent client")
	}

	return agentClient, nil
}

// placeBinaries places agent/monit binaries into /var/vcap/bosh/bin
func (p AgentProvisioner) placeBinaries() error {
	binPath := "/var/vcap/bosh/bin"

	// Implicitly creates /var/vcap/bosh
	err := p.cmds.MkdirP(binPath)
	if err != nil {
		return err
	}

	binNames := map[string]string{
		"agent/bosh-agent":         "bosh-agent",
		"agent/bosh-agent-rc":      "bosh-agent-rc",
		"agent/bosh-blobstore-dav": "bosh-blobstore-dav",
		"monit/monit":              "monit",
	}

	for assetName, binName := range binNames {
		err = p.placeBinary(assetName, filepath.Join(binPath, binName))
		if err != nil {
			return err
		}
	}

	return nil
}

func (p AgentProvisioner) placeBinary(name, path string) error {
	err := p.assetManager.Place(name, path)
	if err != nil {
		return bosherr.WrapErrorf(err, "Placing %s binary", name)
	}

	err = p.cmds.ChmodX(path)
	if err != nil {
		return err
	}

	return nil
}

func (p AgentProvisioner) placeConfFiles() error {
	err := p.setUpDataDir()
	if err != nil {
		return bosherr.WrapError(err, "Setting up data dir")
	}

	fileNames := map[string]string{
		"agent/agent.cert": "agent.cert", // Needed by agent HTTP handler
		"agent/agent.key":  "agent.key",
	}

	for assetName, fileName := range fileNames {
		err := p.assetManager.Place(assetName, filepath.Join("/var/vcap/bosh/", fileName))
		if err != nil {
			return bosherr.WrapErrorf(err, "Placing %s", fileName)
		}
	}

	err = p.placeAgentConf()
	if err != nil {
		return bosherr.WrapError(err, "Placing agent configuration")
	}

	return nil
}

func (p AgentProvisioner) placeAgentConf() error {
	//  etc/plaform is loaded by BOSH Agent runit script
	err := p.fs.WriteFileString("/var/vcap/bosh/etc/platform", p.agentProvisionerConfig.Platform)
	if err != nil {
		return bosherr.WrapError(err, "Writing agent platform")
	}

	// Go Agent will can unmarshal 'null' into an empty config
	bytes, err := json.Marshal(p.agentProvisionerConfig.Configuration)
	if err != nil {
		return bosherr.WrapError(err, "Marshalling agent configuration")
	}

	err = p.fs.WriteFile("/var/vcap/bosh/agent.json", bytes)
	if err != nil {
		return bosherr.WrapError(err, "Writing agent configuration")
	}

	return nil
}

func (p AgentProvisioner) setUpDataDir() error {
	err := p.cmds.Bash("ln -nsf data/sys /var/vcap/sys")
	if err != nil {
		return err
	}

	// todo hacky data dir
	err = p.cmds.MkdirP("/var/vcap/data")
	if err != nil {
		return err
	}

	err = p.cmds.Chmod("777", "/var/vcap/data")
	if err != nil {
		return err
	}

	return nil
}

func (p AgentProvisioner) placeInfSettings(instance bpdep.Instance) error {
	type h map[string]interface{}

	netSettings := map[string]h{}

	for _, netAssoc := range instance.NetworkAssociations {
		netConfig := instance.NetworkConfigurationForNetworkAssociation(netAssoc)

		netSettings[netAssoc.Network.Name] = h{
			"type":    netAssoc.Network.Type,
			"ip":      netConfig.IP,
			"netmask": netConfig.Netmask,
			"gateway": netConfig.Gateway,

			"dns_record_name":  instance.DNDRecordName(netAssoc),
			"cloud_properties": h{},

			"preconfigured": true,
		}
	}

	settings := h{
		"agent_id": fmt.Sprintf("agent-id-%s-%d", instance.JobName, instance.Index),

		"vm": h{
			"name": fmt.Sprintf("vm-name-%s-%d", instance.JobName, instance.Index),
			"id":   fmt.Sprintf("vm-id-%s-%d", instance.JobName, instance.Index),
		},

		"networks": netSettings,
		"disks":    h{"persistent": h{}},

		"blobstore": p.blobstoreConfig,
		"mbus":      p.agentProvisionerConfig.Mbus, // todo port can conflict with jobs

		"env": h{},
		"ntp": []string{},
	}

	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return bosherr.WrapError(err, "Marshalling agent infrastructure settings")
	}

	err = p.fs.WriteFile("/var/vcap/bosh/warden-cpi-agent-env.json", settingsJSON)
	if err != nil {
		return bosherr.WrapError(err, "Writing agent infrastructure settings")
	}

	return nil
}

func (p AgentProvisioner) buildAgentClient() (bpagclient.Client, error) {
	agentClient, err := bpagclient.NewInsecureHTTPClientWithURI(p.agentProvisionerConfig.Mbus, p.logger)
	if err != nil {
		return nil, bosherr.WrapError(err, "Building agent client")
	}

	for i := 0; i < 120; i++ {
		_, err = agentClient.Ping()
		if err == nil {
			return agentClient, nil
		}

		time.Sleep(1 * time.Second)
	}

	return nil, err
}
