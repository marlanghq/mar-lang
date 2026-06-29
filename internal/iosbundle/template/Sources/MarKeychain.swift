// Persistent secret storage for the iOS runtime — currently a single
// slot: the user's session token, set on /_auth/verify-code and
// attached to every subsequent request as `Authorization: Bearer …`.
//
// Why Keychain (and not UserDefaults / file in Documents)?
//   - Keychain is the canonical place for credentials on Apple
//     platforms; the OS encrypts entries at rest and the keys are
//     released only after the user's first device unlock.
//   - Tokens persist through app uninstall+reinstall by default
//     (kSecAttrAccessibleAfterFirstUnlock). That matches the user
//     intent of "I logged in on this device" surviving across rebuilds
//     during development. If we ever want sign-out-on-uninstall, the
//     accessibility constant moves to ThisDeviceOnly + a per-install
//     UUID we sync into the access group.
//   - URLSession's HTTPCookieStorage has a long-standing quirk where
//     cookies with only Max-Age (no Expires) are dropped at app exit.
//     Going Keychain-backed Bearer instead means we never depend on
//     CFNetwork's cookie persistence — and the same code shape will
//     work on Android (EncryptedSharedPreferences) and Windows
//     (Credential Manager) when those runtimes land.
//
// API surface is intentionally tiny: save / load / delete. The
// scoping is by service+account string; we use a single fixed account
// ("session") under the app's bundle id, which means a single token
// per app install. Multi-account would extend the account argument.

import Foundation
import Security

enum MarKeychain {

    /// Key under which the session token lives. Logical name only —
    /// the actual lookup is service+account scoped via Keychain
    /// queries below.
    static let sessionTokenKey = "session"

    /// Persist `value` against `key`. Replaces the existing entry if
    /// present. Failure is logged (DEBUG) and otherwise silent —
    /// failing to persist a token isn't fatal: the user will be asked
    /// to sign in again on next launch, which degrades the experience
    /// but doesn't crash the app.
    static func save(_ value: String, forKey key: String) {
        guard let data = value.data(using: .utf8) else { return }
        let query: [String: Any] = [
            kSecClass as String:       kSecClassGenericPassword,
            kSecAttrService as String: service(),
            kSecAttrAccount as String: key,
        ]
        // Replace-by-upsert pattern: try update first; if no entry,
        // add. The straight "delete then add" path also works but
        // SecItemUpdate is atomic in the keep-attributes-stable case
        // and surfaces fewer error variants.
        let attrsToUpdate: [String: Any] = [
            kSecValueData as String:       data,
            kSecAttrAccessible as String:  kSecAttrAccessibleAfterFirstUnlock,
        ]
        let status = SecItemUpdate(query as CFDictionary, attrsToUpdate as CFDictionary)
        if status == errSecItemNotFound {
            var addQuery = query
            for (k, v) in attrsToUpdate { addQuery[k] = v }
            let addStatus = SecItemAdd(addQuery as CFDictionary, nil)
            #if DEBUG
            if addStatus != errSecSuccess {
                print("[mar][keychain] add failed: \(addStatus)")
            }
            #endif
            return
        }
        #if DEBUG
        if status != errSecSuccess {
            print("[mar][keychain] update failed: \(status)")
        }
        #endif
    }

    /// Read the value previously stored under `key`, or nil if absent.
    /// Errors (corruption, missing, denied) all collapse to nil so the
    /// caller treats them uniformly as "not signed in".
    static func load(forKey key: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String:       kSecClassGenericPassword,
            kSecAttrService as String: service(),
            kSecAttrAccount as String: key,
            kSecReturnData as String:  true,
            kSecMatchLimit as String:  kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess,
              let data = item as? Data,
              let str = String(data: data, encoding: .utf8) else {
            return nil
        }
        return str
    }

    /// Remove the entry under `key`. No-op if absent.
    static func delete(forKey key: String) {
        let query: [String: Any] = [
            kSecClass as String:       kSecClassGenericPassword,
            kSecAttrService as String: service(),
            kSecAttrAccount as String: key,
        ]
        SecItemDelete(query as CFDictionary)
    }

    /// Service identifier — scopes Keychain queries to this app so two
    /// Mar apps on the same device don't share session tokens. Falls
    /// back to a constant string on the off chance bundleIdentifier is
    /// nil (test harnesses, hosted previews); not a real-world path
    /// but the fallback prevents a crash.
    private static func service() -> String {
        Bundle.main.bundleIdentifier ?? "com.marlang.runtime"
    }
}
