package com.example
package renamed

import com.example.core.Base

/** Directory is app/src/main/scala/weird; package is com.example.renamed. The
  * fact NAME is directory-anchored, the fqn follows the package, and the
  * `extends Base` edge resolves through the import either way. */
class Detached extends Base {
  def id: Long = 42L
}
