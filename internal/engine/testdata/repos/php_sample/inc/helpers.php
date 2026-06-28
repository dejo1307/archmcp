<?php
/**
 * Procedural helpers, WordPress-style global functions.
 */

function acme_format($text) {
    return acme_sanitize($text);
}

function acme_sanitize($text) {
    return trim($text);
}
