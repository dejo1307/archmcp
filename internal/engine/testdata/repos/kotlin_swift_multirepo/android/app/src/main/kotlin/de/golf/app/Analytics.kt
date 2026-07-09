package de.golf.app

// Three nested types whose names are shared, by coincidence of domain vocabulary,
// with the Swift app in ../ios. The two repos share no source: each is an
// independent client of the same backend. No cross-repo edge may be drawn between
// them on the strength of these names alone.

class RegisterUseCase {
    data class ValidationError(val field: String, val reason: String)

    fun validate(email: String): ValidationError? {
        if (!email.contains("@")) {
            return ValidationError("email", "missing @")
        }
        return null
    }
}

class HandicapAnalytics {
    data class DifferentialEntry(val score: Double, val courseRating: Double)

    fun differentials(scores: List<Double>): List<DifferentialEntry> {
        return scores.map { DifferentialEntry(it, 72.0) }
    }
}

class FullAnalysisDataBuilder {
    data class TimeWindow(val startDay: Int, val endDay: Int)

    fun lastMonth(): TimeWindow = TimeWindow(0, 30)
}
