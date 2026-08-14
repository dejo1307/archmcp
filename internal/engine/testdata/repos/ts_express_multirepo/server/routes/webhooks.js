// A sub-router whose mount point is in ANOTHER file (index.js mounts it at
// '/webhooks'). Its declared paths are fragments: the real routes are
// '/webhooks/login', not '/login'. Emitting the fragment would be a WRONG fact, and a
// wrong path can false-match another repo's route — worse than silence.
//
// The repo-wide pass (tsextractor/routermount.go) resolves the mount across the two
// files, so these are stored at '/webhooks/login' and carry mount_composed=true. The
// golden pins both the composed paths AND the absence of the bare fragments: a
// half-resolved mount would be the wrong fact this file exists to guard against.
const express = require('express');
const router = express.Router();

router.post('/login', async (req, res) => {
  res.json({ ok: true });
});

router.get('/login', async (req, res) => {
  res.json({ ok: true });
});

module.exports = router;
