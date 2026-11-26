#!/usr/bin/env python3
"""Exercise the dev entry point without a Docker daemon or real Go build."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


HERE = Path(__file__).resolve().parent


class DevGatewayTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="gateway-dev-fixture-")
        self.addCleanup(self.temporary.cleanup)
        self.folder = Path(self.temporary.name).resolve()
        self.root = self.folder / "gateway"
        self.deploy = self.root / "deploy" / "gateway"
        self.deploy.mkdir(parents=True)
        self.script = self.deploy / "dev-gateway.sh"
        shutil.copyfile(HERE / "dev-gateway.sh", self.script)
        shutil.copyfile(HERE / "build-admin-ui.sh", self.deploy / "build-admin-ui.sh")
        (self.root / "backend").mkdir()
        (self.root / "backend" / "go.mod").write_text("module erp-platform/gateway\n\ngo 1.26.11\n")
        (self.root / "frontend").mkdir()
        (self.root / "frontend" / "package-lock.json").write_text("{}")
        self.binary = self.folder / "bin"
        self.binary.mkdir()
        self.home = self.folder / "home"
        self.home.mkdir()
        self.state = self.root / "secrets" / "local-gateway"
        self.calls = self.folder / "calls.jsonl"
        self.runtime = self.folder / "runtime.json"
        self.environment = dict(os.environ, HOME=str(self.home),
                                PATH=str(self.binary) + os.pathsep + os.environ["PATH"],
                                GATEWAY_CONFIG_DIR=str(self.folder / "production-config"),
                                GATEWAY_DATABASE_PATH=str(self.folder / "production.db"),
                                FCM_CREDENTIALS_FILE="must-not-be-inherited",
                                INHERITED_PRODUCTION_SENTINEL="must-not-be-inherited")
        self._executable("uname", "import sys\nprint('Darwin' if sys.argv[1:] == ['-s'] else 'arm64')\n")
        self._write_go()
        self._write_docker()
        self._executable("npm", f"""import json, pathlib, sys
args = sys.argv[1:]
with pathlib.Path({str(self.calls)!r}).open('a') as log:
    log.write(json.dumps({{'tool':'npm','args':args}})+'\\n')
frontend = pathlib.Path(args[args.index('--prefix') + 1])
if 'ci' in args:
    (frontend / 'node_modules').mkdir(exist_ok=True)
elif args[-2:] == ['run', 'build']:
    output = frontend / 'dist' / 'browser'
    output.mkdir(parents=True, exist_ok=True)
    (output / 'index.html').write_text('<base href="/admin/"><app-root ngCspNonce="__GATEWAY_CSP_NONCE__"></app-root>Angular fixture')
else:
    sys.exit('Unexpected npm invocation')
""")

    def _executable(self, name, body):
        path = self.binary / name
        path.write_text("#!" + sys.executable + "\n" + body)
        path.chmod(0o755)

    def _write_go(self, version="1.26.11"):
        runtime_body = "\n".join([
            "#!" + sys.executable,
            "import json, os, pathlib",
            "pathlib.Path(" + repr(str(self.runtime)) + ").write_text(json.dumps({",
            "'config': os.environ.get('GATEWAY_CONFIG_DIR'),",
            "'database': os.environ.get('GATEWAY_DATABASE_PATH'),",
            "'inherited': 'INHERITED_PRODUCTION_SENTINEL' in os.environ,",
            "'fcm': os.environ.get('FCM_CREDENTIALS_FILE'),",
            "'listen': os.environ.get('LISTEN_ADDR')}))",
            "",
        ])
        self._executable("go", f"""import json, os, pathlib, sys
args = sys.argv[1:]
with pathlib.Path({str(self.calls)!r}).open('a') as log:
    log.write(json.dumps({{'tool':'go','args':args,'proxy':os.environ.get('GOPROXY'),'toolchain':os.environ.get('GOTOOLCHAIN'),'gosumdb':os.environ.get('GOSUMDB'),'gowork':os.environ.get('GOWORK')}})+'\\n')
if args == ['version']:
    print('go version go{version} darwin/arm64')
elif args == ['env', 'GOMODCACHE']:
    print({str(self.folder / 'empty-module-cache')!r})
elif args and args[0] == 'build':
    output = pathlib.Path(args[args.index('-o') + 1])
    output.write_text({runtime_body!r})
    output.chmod(0o755)
else:
    sys.exit('Unexpected Go invocation')
""")

    def _write_docker(self, image_arch="arm64"):
        self._executable("docker", f"""import json, os, pathlib, sys
args = sys.argv[1:]
with pathlib.Path({str(self.calls)!r}).open('a') as log:
    log.write(json.dumps({{'tool':'docker','args':args}})+'\\n')
if args[:2] != ['--context', 'desktop-linux']:
    sys.exit('Expected isolated local Docker context')
args = args[2:]
if args[:2] == ['context', 'inspect']:
    print('unix://' + os.environ['HOME'] + '/.docker/run/docker.sock')
elif args and args[0] == 'info':
    print('linux/aarch64')
elif args[:2] == ['image', 'ls']:
    pass
elif args[:2] == ['image', 'inspect']:
    print('linux/{image_arch}')
elif args[:2] == ['volume', 'ls']:
    pass
elif args and args[0] == 'compose' and ('up' in args or 'stop' in args):
    print('Mock local development project ready')
else:
    sys.exit('Unexpected Docker invocation')
""")

    def run_script(self, *arguments):
        return subprocess.run(["bash", str(self.script), *arguments], cwd=self.root,
                              env=self.environment, text=True, capture_output=True, timeout=20)

    def tool_calls(self, tool):
        if not self.calls.exists():
            return []
        return [call for call in map(json.loads, self.calls.read_text().splitlines())
                if call["tool"] == tool]

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)

    def assert_native_isolated(self):
        runtime = json.loads(self.runtime.read_text())
        self.assertEqual(runtime["config"], str(self.state / "native" / "config"))
        self.assertEqual(runtime["database"], str(self.state / "native" / "gateway.db"))
        self.assertEqual(runtime["listen"], "127.0.0.1:8080")
        self.assertFalse(runtime["inherited"])
        self.assertIsNone(runtime["fcm"])
        builds = [call for call in self.tool_calls("go") if call["args"][0] == "build"]
        self.assertEqual(len(builds), 1)
        self.assertEqual(builds[0]["proxy"], "off")
        self.assertEqual(builds[0]["toolchain"], "local")
        self.assertEqual(builds[0]["gosumdb"], "off")
        self.assertEqual(builds[0]["gowork"], "off")
        self.assertEqual(self.tool_calls("docker"), [])
        self.assertIn("Angular fixture", (self.root / "backend/internal/admin/assets/ui/index.html").read_text())
        self.assertEqual(len(self.tool_calls("npm")), 2)

    def test_no_arguments_builds_current_source_without_docker(self):
        self.assert_success(self.run_script())
        self.assert_native_isolated()

    def test_explicit_native_alias_keeps_runtime_configuration_isolated(self):
        self.assert_success(self.run_script("--native"))
        self.assert_native_isolated()

    def test_explicit_image_uses_existing_docker_image_without_pull_or_build(self):
        self.assert_success(self.run_script("--image", "trusted/gateway:fixture"))
        self.assertEqual(self.tool_calls("go"), [])
        self.assertEqual(self.tool_calls("npm"), [])
        calls = self.tool_calls("docker")
        self.assertTrue(any(call["args"][2:4] == ["image", "inspect"] for call in calls))
        up = [call for call in calls if "up" in call["args"]]
        self.assertEqual(len(up), 1)
        self.assertIn("--no-build", up[0]["args"])
        self.assertEqual(up[0]["args"][-2:], ["--pull", "never"])
        self.assertFalse(any("build" in call["args"] or "pull" in call["args"] for call in calls))

    def test_wrong_architecture_fails_before_state_mutation(self):
        self._write_docker(image_arch="amd64")
        result = self.run_script("--image", "trusted/gateway:fixture")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not Linux/arm64", result.stderr)
        self.assertFalse(self.state.exists())
        self.assertFalse(self.runtime.exists())

    def test_native_and_image_are_rejected_before_state_mutation(self):
        result = self.run_script("--native", "--image", "trusted/gateway:fixture")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.state.exists())
        self.assertEqual(self.tool_calls("go"), [])
        self.assertEqual(self.tool_calls("docker"), [])

    def test_empty_image_is_rejected_before_state_mutation(self):
        result = self.run_script("--image", "")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--image needs", result.stderr)
        self.assertFalse(self.state.exists())
        self.assertEqual(self.tool_calls("go"), [])
        self.assertEqual(self.tool_calls("docker"), [])

    def test_stop_retains_docker_dev_data(self):
        self.state.mkdir(parents=True)
        previous = self.state / "compose.env"
        previous.write_text("DEV_GATEWAY_IMAGE=trusted/gateway:fixture\n")
        marker = self.state / "keep-data"
        marker.write_text("preserve")
        self.assert_success(self.run_script("--stop"))
        stops = [call for call in self.tool_calls("docker") if "stop" in call["args"]]
        self.assertEqual(len(stops), 1)
        self.assertEqual(stops[0]["args"][-2:], ["stop", "gateway"])
        self.assertEqual(marker.read_text(), "preserve")
        self.assertTrue(previous.exists())
        self.assertFalse(self.runtime.exists())

    def test_native_config_symlink_is_refused(self):
        native = self.state / "native"
        native.mkdir(parents=True)
        outside = self.folder / "outside-config"
        outside.mkdir()
        (native / "config").symlink_to(outside, target_is_directory=True)
        result = self.run_script("--native")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symlink", result.stderr.lower())
        self.assertFalse(self.runtime.exists())
        self.assertEqual(list(outside.iterdir()), [])

    def test_missing_cached_toolchain_fails_without_creating_state(self):
        self._write_go(version="1.25.0")
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not cached", result.stderr)
        self.assertFalse(self.state.exists())


if __name__ == "__main__":
    unittest.main()
