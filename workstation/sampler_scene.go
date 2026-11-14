package main

import (
	"fmt"
	"math"
	"net/http"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/controller"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/scene"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

// samplerScene supplies playable pad geometry; the DOM buttons remain accessible controls.
func samplerScene() scene.Props {
	nodes := []scene.Node{
		scene.DirectionalLight{ID: "key", Color: "#fff0dd", Intensity: 1.35, Direction: scene.Vec3(-.5, -1, -.7)},
		scene.PointLight{ID: "fill", Color: "#a8bbd0", Intensity: .7, Position: scene.Vec3(3, 5, 4), Range: 20, Decay: 2},
		scene.Mesh{ID: "chassis", Geometry: scene.BoxGeometry{Width: 7.3, Height: .4, Depth: 7.1}, Material: scene.StandardMaterial{Color: "#20252a", Roughness: .38, Metalness: .72}},
		scene.Mesh{ID: "deck", Geometry: scene.BoxGeometry{Width: 7.1, Height: .06, Depth: 6.9}, Material: scene.StandardMaterial{Color: "#363b3e", Roughness: .42, Metalness: .68}, Position: scene.Vec3(0, .22, 0)},
		scene.Mesh{ID: "front-trim", Pickable: scene.Bool(false), Geometry: scene.BoxGeometry{Width: 7.08, Height: .035, Depth: .025}, Material: scene.StandardMaterial{Color: "#697177", Roughness: .48, Metalness: .7}, Position: scene.Vec3(0, .11, 3.555)},
	}
	pad := samplerBevelPad()
	for i := 0; i < 16; i++ {
		x := float64(i%4)*1.7 - 2.55
		z := float64(i/4)*1.65 - 2.475
		id := fmt.Sprint(i + 1)
		nodes = append(nodes,
			scene.Mesh{ID: "socket-" + id, Geometry: scene.BoxGeometry{Width: 1.55, Height: .12, Depth: 1.48}, Material: scene.StandardMaterial{Color: "#111519", Roughness: .8}, Position: scene.Vec3(x, .28, z)},
			scene.Mesh{ID: "pad-" + id, Interactive: true, Geometry: pad, Material: scene.StandardMaterial{Color: "#666b67", Roughness: .87}, Position: scene.Vec3(x, .38, z)},
			scene.Mesh{ID: "light-" + id, Pickable: scene.Bool(false), Geometry: scene.BoxGeometry{Width: 1.16, Height: .012, Depth: .98}, Material: scene.StandardMaterial{Color: "#b78a4b", Roughness: .85}, Position: scene.Vec3(x, -100, z)},
		)
	}
	return scene.Props{Width: 800, Height: 560, PickSignalNamespace: "sampler3d", Responsive: scene.Bool(true), Background: "#141719", Controls: "orbit", Camera: scene.PerspectiveCamera{Position: scene.Vec3(6, 8, 10), FOV: 42}, ControlTarget: scene.Vec3(0, 0, 0), Environment: scene.Environment{AmbientColor: "#ffffff", AmbientIntensity: .35}, Graph: scene.NewGraph(nodes...)}
}

// samplerBevelPad keeps the pad's established interactive geometry.
func samplerBevelPad() scene.BufferGeometry {
	type v struct{ x, y, z float64 }
	ring := func(w, d, c, y float64) []v {
		return []v{{-w + c, y, -d}, {w - c, y, -d}, {w, y, -d + c}, {w, y, d - c}, {w - c, y, d}, {-w + c, y, d}, {-w, y, d - c}, {-w, y, -d + c}}
	}
	lower, shoulder, top := ring(.69, .60, .09, -.09), ring(.69, .60, .09, .035), ring(.63, .54, .09, .09)
	var g scene.BufferGeometry
	tri := func(a, b, c v) {
		ux, uy, uz := b.x-a.x, b.y-a.y, b.z-a.z
		vx, vy, vz := c.x-a.x, c.y-a.y, c.z-a.z
		nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
		l := math.Sqrt(nx*nx + ny*ny + nz*nz)
		for _, p := range []v{a, b, c} {
			g.Positions = append(g.Positions, p.x, p.y, p.z)
			g.Normals = append(g.Normals, nx/l, ny/l, nz/l)
		}
	}
	for i := 0; i < 8; i++ {
		j := (i + 1) % 8
		tri(lower[i], shoulder[j], lower[j])
		tri(lower[i], shoulder[i], shoulder[j])
		tri(shoulder[i], top[j], shoulder[j])
		tri(shoulder[i], top[i], top[j])
		tri(v{0, .09, 0}, top[j], top[i])
	}
	return g
}

// samplerSceneFrame serves only established workspace members and carries no score or sample data.
func (s *studioApp) samplerSceneFrame(ctx *server.Context) gosx.Node {
	ctx.NoStore()
	role := ""
	if store := session.Current(ctx.Request); store != nil {
		actor := store.String("collaboration-user")
		if actor != "" && s.collaboration.access != nil {
			if access, err := s.collaboration.access.membership(actor, false, false); err == nil {
				role = access.Roles[actor]
			}
		} else if actor != "" {
			s.collaboration.mu.Lock()
			role = s.collaboration.roles[actor]
			s.collaboration.mu.Unlock()
		}
	}
	if role != "owner" && role != "editor" && role != "viewer" {
		ctx.SetStatus(http.StatusForbidden)
		return gosx.Text("Workspace session required.")
	}
	// Relay correlated GoSX ray results to the frame engine. This explicit
	// input controller also publishes the optional runtime asset contract.
	ctx.Runtime().Controller(controller.Config{
		Name: "sampler-frame-ray", Root: "#sampler-frame-scene",
		Outputs: []controller.Output{{Name: "ray", Event: "cicada:sampler-ray"}},
		Events: []controller.Event{{Type: "gosx:scene3d:input", Target: "#sampler-frame-scene", Output: "ray", Project: &controller.Projection{
			When:   map[string]any{"event.detail.kind": "ray"},
			Fields: map[string]string{"input": "event.detail.input"},
		}}},
	})
	props := samplerScene().RawPropsJSON()
	surface := ctx.Engine(engine.Config{Name: "GoSXScene3D", Kind: engine.KindSurface, MountID: "sampler-frame-scene", Props: props, Capabilities: []engine.Capability{engine.CapCanvas, engine.CapWebGPU, engine.CapWebGL, engine.CapAnimation}, MountAttrs: map[string]any{"data-gosx-scene3d": true}}, gosx.El("p", gosx.Text("3D unavailable.")))
	return meterEngine(ctx, engine.Config{Name: "CicadaSamplerFrame", Kind: engine.KindSurface, MountID: "sampler-frame-controller", Runtime: engine.RuntimeGoWASM}, surface)
}
