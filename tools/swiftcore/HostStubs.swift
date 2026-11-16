import Foundation

// Stand-ins for the parts of the app that need SwiftUI / Observation / Security,
// so the INTERPRETER core can be compiled and driven from the command line.
//
// Every one of them TRAPS rather than answering. A stub that returns a
// plausible value is the same lie the fake browser told for months: the code
// runs, does nothing, and the test reads as a pass. If a probe reaches one of
// these, the probe is testing something outside the core and should say so.
enum MarStubTrap {
    static func hit(_ what: String) -> Never {
        fatalError("core-only harness: \(what) is outside the interpreter core")
    }
}

final class AppContextStub {
    static let shared = AppContextStub()
    var currentUser: MarValue? {
        get { MarStubTrap.hit("AppContext.currentUser") }
        set { MarStubTrap.hit("AppContext.currentUser=") }
    }
    var signInPath: String {
        get { MarStubTrap.hit("AppContext.signInPath") }
        set { MarStubTrap.hit("AppContext.signInPath=") }
    }
    var pendingReturnPath: String? {
        get { MarStubTrap.hit("AppContext.pendingReturnPath") }
        set { MarStubTrap.hit("AppContext.pendingReturnPath=") }
    }
    var redirectingToSignIn: Bool {
        get { MarStubTrap.hit("AppContext.redirectingToSignIn") }
        set { MarStubTrap.hit("AppContext.redirectingToSignIn=") }
    }
    func navigate(path: String, replace: Bool) { MarStubTrap.hit("AppContext.navigate") }
    func forgetUser() { MarStubTrap.hit("AppContext.forgetUser") }
    func dismissTop() { MarStubTrap.hit("AppContext.dismissTop") }
    func handleAuthExpired() -> Bool { MarStubTrap.hit("AppContext.handleAuthExpired") }
    func capturePages(_ v: MarValue) { MarStubTrap.hit("AppContext.capturePages") }
}
enum AppContext { static var shared: AppContextStub { AppContextStub.shared } }

final class SharedSlotStub { func dispatch(_ m: MarValue) { MarStubTrap.hit("shared.dispatch") } }
enum MarSharedRegistry {
    static func nextKey() -> String { MarStubTrap.hit("MarSharedRegistry.nextKey") }
    static func store(key: String, initFn: MarValue, updateFn: MarValue, subscriptionsFn: MarValue) -> SharedSlotStub {
        MarStubTrap.hit("MarSharedRegistry.store")
    }
    static func lookup(_ key: String) -> SharedSlotStub? { MarStubTrap.hit("MarSharedRegistry.lookup") }
}

enum MarKeychain {
    static let sessionTokenKey = "mar.session"
    static func save(_ v: String, forKey k: String) { MarStubTrap.hit("MarKeychain.save") }
    static func load(forKey k: String) -> String? { MarStubTrap.hit("MarKeychain.load") }
    static func delete(forKey k: String) { MarStubTrap.hit("MarKeychain.delete") }
}

enum MarSound { static func register(_ env: Env) {} }
enum MarInput { static func register(_ env: Env) {} }
enum MarCanvas { static func register(_ env: Env) {} }
