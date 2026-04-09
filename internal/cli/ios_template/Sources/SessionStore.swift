import Foundation

final class SessionStore {
    private let sessionKey = "MarRuntimeIOS.Session"
    private let schemaKey = "MarRuntimeIOS.Schema"

    func load() -> SessionSnapshot? {
        guard
            let data = UserDefaults.standard.data(forKey: sessionKey),
            let session = try? JSONDecoder().decode(SessionSnapshot.self, from: data)
        else {
            return nil
        }
        return session
    }

    func save(_ session: SessionSnapshot) {
        if let data = try? JSONEncoder().encode(session) {
            UserDefaults.standard.set(data, forKey: sessionKey)
        }
    }

    func clear() {
        UserDefaults.standard.removeObject(forKey: sessionKey)
    }

    func loadSchema(baseURL: String) -> SchemaCacheSnapshot? {
        guard
            let data = UserDefaults.standard.data(forKey: schemaKey),
            let snapshot = try? JSONDecoder().decode(SchemaCacheSnapshot.self, from: data),
            snapshot.baseURL == baseURL
        else {
            return nil
        }
        return snapshot
    }

    func saveSchema(_ snapshot: SchemaCacheSnapshot) {
        if let data = try? JSONEncoder().encode(snapshot) {
            UserDefaults.standard.set(data, forKey: schemaKey)
        }
    }
}
