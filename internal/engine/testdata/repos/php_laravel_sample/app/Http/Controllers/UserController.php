<?php

namespace App\Http\Controllers;

use Illuminate\Support\Facades\Http;

class UserController
{
    public function index()
    {
        // Outbound HTTP-client call to a downstream service.
        $base = getenv('BILLING_BASE_URL');
        return Http::get('/invoices/summary');
    }

    public function store()
    {
        return Http::post('/users');
    }
}
