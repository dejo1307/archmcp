<?php

namespace App\Services;

use GuzzleHttp\Client;

// Outbound HTTP calls to the billing service. These match the provider's
// GET/POST /billing/invoices server routes, so the cross-repo linker draws a
// consumer -> provider dependency edge (via http-client).
class BillingClient
{
    private Client $client;

    public function listInvoices()
    {
        return $this->client->get('/billing/invoices');
    }

    public function createInvoice(array $payload)
    {
        return $this->client->post('/billing/invoices', ['json' => $payload]);
    }
}
