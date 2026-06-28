<?php

use App\Http\Controllers\InvoiceController;
use Illuminate\Support\Facades\Route;

// Served by the billing service; the consumer calls the first two. The {id}
// show route is intentionally not called by any loaded client, so it surfaces
// as an unused-route candidate.
Route::get('/billing/invoices', [InvoiceController::class, 'index']);
Route::post('/billing/invoices', [InvoiceController::class, 'store']);
Route::get('/billing/invoices/{id}', [InvoiceController::class, 'show']);
