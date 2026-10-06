# Cicada Game Director

Drive Cicada game music by state, macro, and one-shot stinger names.
This package ships JavaScript and TypeScript types with no runtime dependencies.

```js
import { GameDirector, workletSender } from '@cicada/game-director';
const game = new GameDirector(manifest, workletSender(musicNode.port));
game.setup();
game.setState('explore');
game.setMacro('intensity', 0.9);
game.setState('combat');
game.triggerStinger('pickup');
```

Load a matching kernel and project image, install setup before `OpPlay`, and
forward decoded kernel messages to `game.handle`. The manifest comes from
`project.DirectorSurfaceOf` in the Go module. Tick arguments use `bigint` and
are absolute 960-PPQ transport ticks; omitted ticks submit now.

See the [SDK reference and game examples](https://github.com/odvcencio/cicada/blob/main/docs/manual/game-director.md)
for initialization, message-buffer handling, scheduling, and error reporting.

For headphone positioning, pass the capability bits from the worklet's ready
message to `SpatialAudio`:

```js
import { SpatialAudio, workletSender } from '@cicada/game-director';
const spatial = new SpatialAudio(workletSender(musicNode.port), trackCount, ready.p);
spatial.trackPosition(0, 1, 0, 0); // metres: X forward, Y left, Z up
spatial.listenerPosition(0, 0, 0);
spatial.listenerRotation(0.5, 0, 0); // yaw, pitch, roll in radians
spatial.trackStereo(0); // restore stereo routing
```

The constructor rejects kernels without `CapabilitySpatial` (bit 17).
Coordinates are finite and within ±10,000 metres; rotation components are
within ±2π. Commands use the same tick arguments as the director. The decoder
uses a generic ear model. See [track positioning](https://github.com/odvcencio/cicada/blob/main/docs/manual/engine-host.md#position-tracks-for-headphones)
for bus conventions, distance gain, and routing.

Run `npm test` in this directory. Create an installable tarball with `npm pack`.
