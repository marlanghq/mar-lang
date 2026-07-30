# Shared cart

Four screens and one cart. The example for `App.shared` — the client state that
outlives navigation.

```
mar dev examples/shared-cart
```

## What it shows

Page models live on the navigation stack: going somewhere new re-runs that
screen's `init`, coming Back hands you the screen you left (ADR 0009). That is
deliberate, and it leaves one question open — where does state that must survive
the trip live? Here.

Try it in this order:

1. Type something into the catalog's **Filter** box and add a couple of items.
2. Open a product, then press **Back**. The filter is still there.
3. From the product, follow **Go to cart**, then **Keep shopping**. You arrived
   at the catalog by a link this time, so the filter is empty — and the cart is
   exactly as you left it.
4. Open **Settings** and turn on tax. Walk to the cart. The total is already
   right: nothing refetched, no message passed between screens, no second copy
   to keep in sync.

The filter and the "Added" note are page state. The cart and the tax preference
are shared. That is the whole lesson.

## How it is wired

`Frontend/Global.mar` holds the store: a `Model`, a `Msg`, and one binding.

```elm
def : App.Shared Model Msg
def =
    App.shared { init = init, update = update, subscriptions = subscriptions }
```

A page **reads** it by wrapping any of the six page constructors:

```elm
page : Page
page =
    Page.withShared Frontend.Global.def
        (\global ->
            Page.create { path = "/", ..., view = view global }
        )
```

and **writes** to it the same way it talks to itself — by sending a message:

```elm
AddClicked id ->
    ( model, Cmd.toShared Frontend.Global.def (Frontend.Global.Added id) )
```

`Main.mar` never mentions the store. There is no registration step: the runtime
finds it through the pages that use it, and those pages agree on the types
because they all name the same `def`.

The builder is re-applied whenever the store changes, so `view global` reads the
live value rather than a copy taken when the page mounted. The page's own model
is untouched and its `update` never runs — a shared change is a repaint, not a
message.

## Files

| | |
| --- | --- |
| `Frontend/Global.mar` | the store: model, messages, `def`, and the derived totals |
| `Frontend/Products.mar` | the catalog as plain data |
| `Frontend/Routes.mar` | typed paths |
| `Frontend/Catalog.mar` | reads and writes the cart; keeps its filter in a page model |
| `Frontend/Item.mar` | a dynamic route — `Page.withShared` wraps `Page.dynamic` the same way |
| `Frontend/Cart.mar` | a page with no model of its own, purely a view onto the store |
| `Frontend/Settings.mar` | flips a preference every other screen reads |

Prices are integer cents, so the totals stay exact.
