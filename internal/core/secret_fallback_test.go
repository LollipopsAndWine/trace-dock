package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestLiteralSecretFallbackDetectsAssignmentsAndEnvDefaults(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"password default":         `DB_PASSWORD = os.getenv("DB_PASSWORD", "UnitPass9#case")`,
		"api key default":          `DB_API_KEY = os.getenv("DB_API_KEY", "unit_user:UnitPass9#case")`,
		"standalone getenv":        `connect(os.getenv("DB_PASSWORD", "UnitPass9#case"))`,
		"imported getenv":          `getenv('SERVICE_API_KEY', 'UnitKey8#case')`,
		"environ get":              `os.environ.get("SERVICE_API_KEY", "UnitKey8#case")`,
		"keyword default":          `os.getenv("DB_PASSWORD", default="UnitPass9#case")`,
		"different env name":       `PASSWORD = os.getenv("DB_LOGIN", "UnitPass9#case")`,
		"quoted assignment":        `password = "UnitPass9#case"`,
		"single quoted assignment": `api_key = 'UnitKey8#case'`,
		"json password":            `{"password":"UnitPass9#case"}`,
		"json api key":             `{"api-key":"unit_user:UnitPass9#case"}`,
		"bracket assignment":       `config["password"] = "UnitPass9#case"`,
		"unquoted env":             "DB_PASSWORD=UnitPass9#case\n",
		"unquoted env at eof":      `DB_API_KEY=unit_user:UnitPass9#case`,
		"weak numeric password":    `password="123456"`,
		"escaped quote in value":   `password="Unit\"Pass9#case"`,
		"multiline default":        "os.getenv(\n    \"DB_PASSWORD\",\n    \"UnitPass9#case\"\n)",
	} {
		t.Run(name, func(t *testing.T) {
			// Trajectories can contain notebook source serialized multiple times.
			for layer := 0; layer < 4; layer++ {
				payload := []byte(source)
				if !scanner.Hit(payload) {
					t.Fatalf("literal credential missed with %d JSON encoding layers", layer)
				}
				hit, err := scanner.Copy(io.Discard, bytes.NewReader(payload))
				if err != nil || !hit {
					t.Fatalf("stream detection at layer %d: hit=%t err=%v", layer, hit, err)
				}
				next, _ := json.Marshal(map[string]string{"output": source})
				source = string(next)
			}
		})
	}
}

func TestLiteralSecretFallbackSkipsReferencesEmptyDefaultsAndExamples(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`api_key=MILVUS_API_KEY`,
		`PASSWORD=os.getenv("DB_PASSWORD")`,
		`PASSWORD=os.getenv("DB_PASSWORD", "")`,
		`API_KEY=os.getenv("SERVICE_API_KEY", "   ")`,
		`PASSWORD=os.getenv("DB_PASSWORD", DEFAULT_PASSWORD)`,
		`PASSWORD=os.getenv("DB_PASSWORD", "prefix_" + DEFAULT_PASSWORD)`,
		`password = "prefix_" + DEFAULT_PASSWORD`,
		`password = settings.password`,
		`password: str`,
		`token = build_token()`,
		`api_key = "example_test_key"`,
		`secret_key=credentials["SecretAccessKey"]`,
		`secret_key = token["clientSecret"]`,
		`api_key: Optional[str] = None`,
		`password: Optional[str]`,
		`PWD = POWER/2`,
		"if pwd:\n    try:\n        tarinfo.uname = pwd.getpwuid(tarinfo.uid)[0]",
		`CHALLENGE_PASSWORD = ObjectIdentifier("1.2.840.113549.1.9.7")`,
		`AttributeOID.CHALLENGE_PASSWORD: "challengePassword"`,
		`password = User.password`,
		`Password = "h]6EszR}vJ*m"`,
		`api_key = "API_KEY_GOES_HERE"`,
		`password = "test_password"`,
		`password = "your_password"`,
		`password = "<PASSWORD>"`,
		`password = "${DB_PASSWORD}"`,
		`api_key = "MILVUS_API_KEY"`,
		`password = "********"`,
		`os.getenv("APPLICATION_NAME", "UnitApplication9")`,
		`PASSWORD=os.getenv("DB_PASSWORD", "example_password")`,
	} {
		for layer := 0; layer < 4; layer++ {
			var output bytes.Buffer
			hit, err := scanner.Copy(&output, strings.NewReader(source))
			if hit || err != nil || output.String() != source || scanner.Hit([]byte(source)) {
				t.Fatalf("reference/example matched or content changed at JSON layer %d: hit=%t err=%v source=%s", layer, hit, err, source)
			}
			next, _ := json.Marshal(map[string]string{"output": source})
			source = string(next)
		}
	}
}

func TestLiteralSecretFallbackCrossesReadBoundariesWithoutChangingCleanFiles(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	secret := `DB_PASSWORD = os.getenv("DB_PASSWORD", "UnitPass9#case")`
	for _, boundary := range []int{3, 14, 27, 39, 47} {
		payload := strings.Repeat(" ", (64<<10)-boundary) + secret
		hit, err := scanner.Copy(io.Discard, strings.NewReader(payload))
		if err != nil || !hit {
			t.Fatalf("missed credential at read split %d: hit=%t err=%v", boundary, hit, err)
		}
	}
	clean := strings.Repeat(" ", (64<<10)-7) + `PASSWORD=os.getenv("DB_PASSWORD", "")` + strings.Repeat(" ", 64<<10)
	var copied bytes.Buffer
	hit, err := scanner.CopyContext(context.Background(), &copied, strings.NewReader(clean))
	if err != nil || hit || copied.String() != clean {
		t.Fatalf("clean file modified: hit=%t err=%v", hit, err)
	}
}

func TestFallbackRejectsRealWorldSDKCodeFalsePositives(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`secret_key = credentials["SecretAccessKey"]`,
		`clientSecret=token["clientSecret"]`,
		`password: Optional[str]`,
		`api_key: Optional[str] = None`,
		`PWD = POWER/2`,
		`password = User.password`,
		`user = User(username="scolvin", password="password1")`,
		`model = Model(password="IAmSensitive", password_bytes=b"IAmSensitiveBytes")`,
		`"Password": "pasword"`,
		`"Password": "SomeKindOfPassword123!@#"`,
		`proxy_password=self._socks_options["password"]`,
		`api_key: "API_KEY_GOES_HERE"`,
	} {
		var copied bytes.Buffer
		hit, err := scanner.Copy(&copied, strings.NewReader(source))
		if err != nil || hit || scanner.Hit([]byte(source)) || copied.String() != source {
			t.Errorf("SDK/example code was flagged or modified: hit=%t err=%v source=%q", hit, err, source)
		}
	}
}
