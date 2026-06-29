package runtime

import "testing"

// Effect.batch is the fire-and-forget fan-out: running the batch must
// run EVERY child effect (each child delivers through its own toMsg on
// the frontend; here we just count executions), and the batch's own
// value is unit, the same dynamic shape Effect.none uses.
func TestEffectBatchRunsAllChildren(t *testing.T) {
	ran := 0
	child := func() Value {
		return VEffect{Tag: "test", Run: func() (Value, error) {
			ran++
			return VUnit{}, nil
		}}
	}

	env := BaseEnv()
	batch, ok := env.Lookup("effectBatch")
	if !ok {
		t.Fatal("effectBatch builtin not registered")
	}
	out, err := Apply(batch, VList{Elements: []Value{child(), child(), child()}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	eff, ok := out.(VEffect)
	if !ok {
		t.Fatalf("Effect.batch should return an Effect, got %T", out)
	}
	if ran != 0 {
		t.Fatalf("children must not run before the batch itself runs (lazy); ran=%d", ran)
	}
	v, err := eff.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ran != 3 {
		t.Fatalf("batch should run all 3 children, ran=%d", ran)
	}
	if _, isUnit := v.(VUnit); !isUnit {
		t.Fatalf("batch's own value should be unit, got %T", v)
	}
}
