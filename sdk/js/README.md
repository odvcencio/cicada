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

Run `npm test` in this directory. Create an installable tarball with `npm pack`.
