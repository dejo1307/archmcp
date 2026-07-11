package com.example.repo;

import java.util.List;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.data.jpa.repository.Query;

// v110: a Spring Data repository interface. Every declared method is a DB
// round-trip, so each carries io_direct/performs_io (GAP-JV-02). findByOwner
// exercises the interface path (isSpringDataRepository); searchByName also the
// @Query annotation path.
public interface WidgetRepository extends JpaRepository<Widget, Long> {

    List<Widget> findByOwner(Long ownerId);

    @Query("select w from Widget w where w.name = ?1")
    Widget searchByName(String name);
}
