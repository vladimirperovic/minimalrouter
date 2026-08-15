package firmware

import "testing"

func completeManifestForArchForTest(arch string) *FirmwareManifest {
	files := map[string]string{}
	for _, path := range []string{
		"web/dist/index.html",
		"slot-exec",
		"compatibility.json",
		"install.sh",
		"init.d/routerd",
		"init.d/router-applyd",
		"init.d/pppoe-wan",
		"sysctl/99-minimalrouter.conf",
		"modules/minimalrouter.conf",
		"logrotate/minimalrouter",
		"ip-up.d-minimalrouter-qos",
		"bin/routerd-" + arch,
		"bin/router-applyd-" + arch,
		"bin/router-recovery-" + arch,
		"bin/router-update-" + arch,
	} {
		files[path] = "00"
	}
	return &FirmwareManifest{Version: "1.2.3", Files: files}
}

func completeAMD64ManifestForTest() *FirmwareManifest {
	return completeManifestForArchForTest("amd64")
}

func TestValidateAppliancePayloadAcceptsCompleteSingleArchitecture(t *testing.T) {
	if err := ValidateAppliancePayload(completeAMD64ManifestForTest()); err != nil {
		t.Fatalf("complete payload rejected: %v", err)
	}
}

func TestValidateAppliancePayloadRejectsMissingSystemIntegration(t *testing.T) {
	manifest := completeAMD64ManifestForTest()
	delete(manifest.Files, "ip-up.d-minimalrouter-qos")
	if err := ValidateAppliancePayload(manifest); err == nil {
		t.Fatal("payload missing PPP integration was accepted")
	}
}

func TestValidateAppliancePayloadRejectsMissingCompatibilityABI(t *testing.T) {
	manifest := completeAMD64ManifestForTest()
	delete(manifest.Files, "compatibility.json")
	if err := ValidateAppliancePayload(manifest); err == nil {
		t.Fatal("payload missing compatibility ABI was accepted")
	}
}

func TestValidateAppliancePayloadRejectsPartialArchitecture(t *testing.T) {
	manifest := completeAMD64ManifestForTest()
	delete(manifest.Files, "bin/router-applyd-amd64")
	if err := ValidateAppliancePayload(manifest); err == nil {
		t.Fatal("payload missing privileged helper binary was accepted")
	}
}

func TestValidateAppliancePayloadRejectsMixedArchitectures(t *testing.T) {
	manifest := completeAMD64ManifestForTest()
	for _, path := range []string{
		"bin/routerd-arm64",
		"bin/router-applyd-arm64",
		"bin/router-recovery-arm64",
		"bin/router-update-arm64",
	} {
		manifest.Files[path] = "00"
	}
	if err := ValidateAppliancePayload(manifest); err == nil {
		t.Fatal("payload containing multiple architecture sets was accepted")
	}
}
