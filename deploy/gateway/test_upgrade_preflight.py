#!/usr/bin/env python3
"""Isolated preflight fixtures. Never executes Docker or the production installer."""
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("upgrade_guard", Path(__file__).with_name("verify-gateway-upgrade.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class UpgradePreflightTests(unittest.TestCase):
    def setUp(self):
        self.old = {"Config": {"Labels": {"com.docker.compose.project": "erp-mobile-gateway", "com.docker.compose.service": "gateway"}, "Env": ["GATEWAY_ENCRYPTION_KEY=synthetic-original-key", "GATEWAY_ADMIN_USERNAME=admin", "GATEWAY_ADMIN_PASSWORD=synthetic-original-password", "GATEWAY_CLIENT_TLS_CERT_FILE=/run/gateway-tls/client-chain.pem", "GATEWAY_CLIENT_TLS_KEY_FILE=/run/gateway-tls/client-key.pem"]}, "Mounts": [{"Type": "volume", "Name": "erp-mobile-gateway_gateway-data", "Destination": "/data", "RW": True}, {"Type": "bind", "Source": "/private/client-chain.pem", "Destination": "/run/gateway-tls/client-chain.pem", "RW": False}, {"Type": "bind", "Source": "/private/client-key.pem", "Destination": "/run/gateway-tls/client-key.pem", "RW": False}]}
        self.new = {"services": {"gateway": {"environment": dict(v.split("=", 1) for v in self.old["Config"]["Env"]), "volumes": [{"type": "volume", "source": "gateway-data", "target": "/data"}, {"type": "bind", "source": "/private/client-chain.pem", "target": "/run/gateway-tls/client-chain.pem", "read_only": True}, {"type": "bind", "source": "/private/client-key.pem", "target": "/run/gateway-tls/client-key.pem", "read_only": True}, {"type": "volume", "source": "gateway-config", "target": "/config"}]}}, "volumes": {"gateway-data": {"name": "erp-mobile-gateway_gateway-data"}, "gateway-config": {"name": "erp-mobile-gateway_gateway-config"}}}

    def test_old_base_tls_requires_explicit_compatibility(self):
        with self.assertRaisesRegex(guard.UpgradeRejected, "legacy-tls"):
            guard.check_upgrade(self.old, self.new, False)
        guard.check_upgrade(self.old, self.new, True)

    def test_original_identity_and_data_are_preserved(self):
        guard.check_upgrade(self.old, self.new, True)
        for change in ("key", "certificate", "data_volume", "mount_policy", "omitted_bind"):
            with self.subTest(change=change):
                candidate = copy.deepcopy(self.new)
                if change == "key":
                    candidate["services"]["gateway"]["environment"]["GATEWAY_ENCRYPTION_KEY"] = "replacement"
                elif change == "certificate":
                    candidate["services"]["gateway"]["volumes"][1]["source"] = "/new/client.pem"
                elif change == "data_volume":
                    candidate["volumes"]["gateway-data"]["name"] = "new-empty-volume"
                elif change == "mount_policy":
                    candidate["services"]["gateway"]["volumes"][2]["read_only"] = False
                else:
                    candidate["services"]["gateway"]["volumes"].pop(2)
                with self.assertRaises(guard.UpgradeRejected):
                    guard.check_upgrade(self.old, candidate, True)

    def test_blank_fresh_template_cannot_replace_existing_credentials(self):
        self.new["services"]["gateway"]["environment"] = {}
        with self.assertRaisesRegex(guard.UpgradeRejected, "GATEWAY_ENCRYPTION_KEY"):
            guard.check_upgrade(self.old, self.new, True)

    def test_persisted_config_volume_cannot_be_lost(self):
        self.old["Mounts"].append({"Type": "volume", "Name": "erp-mobile-gateway_gateway-config", "Destination": "/config", "RW": True})
        guard.check_upgrade(self.old, self.new, True)
        self.new["volumes"]["gateway-config"]["name"] = "other-config"
        with self.assertRaises(guard.UpgradeRejected):
            guard.check_upgrade(self.old, self.new, True)

    def test_database_path_and_release_trust_cannot_change_silently(self):
        for key in ("GATEWAY_DATABASE_PATH", "GATEWAY_CONFIG_DIR", "GATEWAY_UPDATE_SIGNING_PUBLIC_KEY", "GATEWAY_RELEASE_PUBLISH_TOKEN_SHA256"):
            with self.subTest(key=key):
                before=copy.deepcopy(self.old)
                before["Config"]["Env"].append(key+"=synthetic-original-value")
                with self.assertRaisesRegex(guard.UpgradeRejected,key):
                    guard.check_upgrade(before,self.new,True)

    def test_no_secret_values_in_errors(self):
        self.new["services"]["gateway"]["environment"] = {}
        try:
            guard.check_upgrade(self.old, self.new, True)
        except guard.UpgradeRejected as error:
            self.assertNotIn("synthetic-original-key", str(error))
            self.assertNotIn("synthetic-original-password", str(error))


if __name__ == "__main__":
    unittest.main()
