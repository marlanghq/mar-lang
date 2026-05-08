// Bonjour / mDNS discovery of mar backends on the local network.
//
// On launch, AppViewModel asks Discovery to start a browse for
// `_mar._tcp`. Each server found is published as a DiscoveredServer.
// When the user hasn't pinned a MarBaseURL via Settings yet,
// AppViewModel auto-selects the first one resolved.
//
// Network framework choices:
//   - NWBrowser is the supported Bonjour API on modern iOS. It
//     gives us TXT records + endpoint resolution out of the box and
//     runs cleanly under Swift concurrency.
//
// The app declares NSBonjourServices = [_mar._tcp] in Info.plist so
// iOS allows the browse without prompting (the prompt is governed by
// NSLocalNetworkUsageDescription, which we also set).

import Foundation
import Network
import Observation

struct DiscoveredServer: Identifiable, Equatable, Hashable, Sendable {
    let name: String
    let host: String
    let port: Int

    /// Stable id for SwiftUI ForEach. host:port is enough — same
    /// physical service on the LAN always resolves to the same URL.
    var id: String { "\(host):\(port)" }
    var url: URL? { URL(string: "http://\(host):\(port)") }
}

@MainActor
@Observable
final class Discovery {
    private(set) var servers: [DiscoveredServer] = []
    private(set) var isBrowsing: Bool = false

    /// Fired on the main actor whenever `servers` changes. AppViewModel
    /// uses this to auto-pick the first resolved server. Plain callback
    /// (instead of an AsyncSequence) keeps the wiring obvious and
    /// avoids spawning a background observation task.
    var onServersChanged: (@MainActor () -> Void)?

    @ObservationIgnored
    private var browser: NWBrowser?

    func start() {
        guard browser == nil else { return }
        let params = NWParameters()
        params.includePeerToPeer = true
        let descriptor = NWBrowser.Descriptor.bonjour(
            type: "_mar._tcp",
            domain: nil
        )
        let nb = NWBrowser(for: descriptor, using: params)
        browser = nb

        nb.stateUpdateHandler = { [weak self] state in
            Task { @MainActor in
                switch state {
                case .ready, .setup:
                    self?.isBrowsing = true
                case .cancelled, .failed:
                    self?.isBrowsing = false
                default:
                    break
                }
            }
        }

        nb.browseResultsChangedHandler = { [weak self] results, _ in
            Task { @MainActor in
                self?.update(results: results)
            }
        }

        nb.start(queue: .main)
    }

    private func update(results: Set<NWBrowser.Result>) {
        var found: [DiscoveredServer] = []
        for r in results {
            guard case let .service(name, _, _, _) = r.endpoint else { continue }
            // NWBrowser hands us the service identity; we still need
            // a concrete host:port. We resolve via NWConnection and
            // merge the entry once the path is ready. The Task
            // inherits the @MainActor isolation of the enclosing
            // type, so no extra hop is needed when calling `merge`.
            let endpoint = r.endpoint
            Task { [weak self] in
                guard let resolved = await Self.resolve(endpoint: endpoint, name: name) else { return }
                self?.merge(resolved)
            }
            // Even before resolution, surface the name so the
            // Settings tab can show "Resolving…" instead of empty.
            found.append(DiscoveredServer(name: name, host: "", port: 0))
        }
        // Replace placeholders only if we have nothing yet; otherwise
        // preserve already-resolved entries (they get refreshed by merge).
        if servers.isEmpty {
            servers = found
            onServersChanged?()
        }
    }

    private func merge(_ s: DiscoveredServer) {
        if let idx = servers.firstIndex(where: { $0.name == s.name }) {
            servers[idx] = s
        } else {
            servers.append(s)
        }
        onServersChanged?()
    }

    /// Resolve a Bonjour endpoint to host:port by opening a transient
    /// NWConnection and reading the path's remote endpoint once the
    /// connection reaches `.ready`. We cancel the connection
    /// immediately after — we only need the resolved address.
    ///
    /// Concurrency: the resolution latch (`done`) is wrapped in a
    /// small actor so the state callback (queue: .main) and the
    /// 5-second timeout (Task.sleep) can both safely race for it
    /// under Swift strict-concurrency checking.
    private static func resolve(endpoint: NWEndpoint, name: String) async -> DiscoveredServer? {
        let latch = ContinuationLatch<DiscoveredServer?>()
        let conn = NWConnection(to: endpoint, using: .tcp)

        return await withCheckedContinuation { (cont: CheckedContinuation<DiscoveredServer?, Never>) in
            Task { await latch.attach(cont) }

            conn.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    let resolved: DiscoveredServer? = {
                        guard let remote = conn.currentPath?.remoteEndpoint,
                              case let .hostPort(host, port) = remote else { return nil }
                        let h = host.debugDescription
                        // Strip trailing %iface suffix that some IPv6
                        // hosts include (e.g. "fe80::1%en0").
                        let cleaned = h.split(separator: "%").first.map(String.init) ?? h
                        return DiscoveredServer(name: name, host: cleaned, port: Int(port.rawValue))
                    }()
                    Task { await latch.fire(resolved); conn.cancel() }
                case .failed, .cancelled:
                    Task { await latch.fire(nil); conn.cancel() }
                default:
                    break
                }
            }
            conn.start(queue: .main)

            // Safety net — bail if resolution takes too long.
            Task {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                await latch.fire(nil)
                conn.cancel()
            }
        }
    }
}

/// Tiny actor used by `Discovery.resolve` to make sure exactly one of
/// the racers (NWConnection ready / failed callback, or the timeout
/// task) gets to resume the continuation.
private actor ContinuationLatch<T: Sendable> {
    private var cont: CheckedContinuation<T, Never>?

    func attach(_ c: CheckedContinuation<T, Never>) {
        cont = c
    }

    func fire(_ value: T) {
        guard let c = cont else { return }
        cont = nil
        c.resume(returning: value)
    }
}
