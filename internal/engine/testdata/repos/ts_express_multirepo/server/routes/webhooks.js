// A sub-router whose mount point is in ANOTHER file (index.js mounts it at
// '/webhooks'). Its declared paths are fragments: the real routes are
// '/webhooks/login', not '/login'. Emitting the fragment would be a WRONG fact, and a
// wrong path can false-match another repo's route — worse than silence. So this file
// contributes no route facts at all, and the golden pins that absence.
//
// Cross-file mount resolution needs a repo-wide pass (the shape of
// goextractor/routeprefix.go); it is deliberately not attempted.
const express = require('express');
const router = express.Router();

router.post('/login', async (req, res) => {
  res.json({ ok: true });
});

router.get('/login', async (req, res) => {
  res.json({ ok: true });
});

module.exports = router;
