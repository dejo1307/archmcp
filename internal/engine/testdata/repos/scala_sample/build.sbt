ThisBuild / scalaVersion := "3.3.1"

lazy val core = project.in(file("core"))
lazy val app  = project.in(file("app")).dependsOn(core)
