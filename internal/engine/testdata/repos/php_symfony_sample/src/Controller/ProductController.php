<?php

namespace App\Controller;

use Symfony\Component\HttpClient\HttpClientInterface;
use Symfony\Component\Routing\Annotation\Route;

#[Route('/products')]
class ProductController
{
    private HttpClientInterface $httpClient;

    #[Route('/{id}', methods: ['GET'], name: 'product_show')]
    public function show($id)
    {
        // Outbound call to an inventory service.
        return $this->httpClient->request('GET', '/inventory/levels');
    }

    #[Route('', methods: ['POST'], name: 'product_create')]
    public function create()
    {
        return [];
    }
}
