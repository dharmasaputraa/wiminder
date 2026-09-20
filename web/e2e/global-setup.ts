import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

export default function globalSetup() {
  const bin = fileURLToPath(new URL('../../bin/wiminder', import.meta.url))
  if (!existsSync(bin)) {
    throw new Error(
      `bin/wiminder not found at ${bin}. Run "make build" first (or use "make e2e"), ` +
        'which builds the SPA into internal/api/webroot and the Go binary.',
    )
  }
}
