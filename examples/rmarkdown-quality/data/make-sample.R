# Regenerates data/deliveries.csv, the sample this report profiles.
#
# Committed so the file in the repository is reproducible rather than mysterious,
# and seeded so it never changes. It is *deliberately* dirty: missing values,
# duplicate keys, inconsistent category spellings, impossible durations and a
# handful of extreme costs. A data quality report over clean data says nothing.
#
# Not at the repository root, and that matters: a root .R file is scanned for
# plumber annotations, so a helper script left beside the document could decide
# the whole repository's framework. Helpers live in a subdirectory.
#
#   Rscript data/make-sample.R

set.seed(56)

n <- 1200
regions <- c("North", "South", "Midlands", "Scotland", "Wales")
carriers <- c("Ridgeway", "Halcyon", "Penfold", "Kestrel")

start <- as.Date("2026-01-06")
df <- data.frame(
  order_id = sprintf("ORD-%05d", seq_len(n) + 10000),
  ordered_at = format(start + sample(0:180, n, replace = TRUE), "%Y-%m-%d"),
  region = sample(regions, n, replace = TRUE, prob = c(.28, .24, .22, .15, .11)),
  carrier = sample(carriers, n, replace = TRUE),
  weight_kg = round(rlnorm(n, meanlog = 0.9, sdlog = 0.7), 2),
  distance_km = round(rgamma(n, shape = 2.2, scale = 60), 1),
  stringsAsFactors = FALSE
)
df$delivery_days <- pmax(1, round(rnorm(n, mean = 2.6 + df$distance_km / 400, sd = 0.9)))
df$cost_gbp <- round(3.2 + 0.9 * df$weight_kg + 0.014 * df$distance_km +
                       rnorm(n, 0, 0.8), 2)
df$status <- sample(c("delivered", "delivered", "delivered", "delivered",
                      "returned", "lost"), n, replace = TRUE)

# --- the defects, each one a thing the report is supposed to notice -----------

# 1. Missing weights, as a scale that failed to report.
df$weight_kg[sample(n, 47)] <- NA

# 2. Missing region on a few rows, which is the field people group by.
df$region[sample(n, 12)] <- NA

# 3. Inconsistent spellings of one category — the classic silent grouping bug.
spell <- sample(which(df$region == "North"), 38)
df$region[spell] <- sample(c("north", "NORTH", " North"), length(spell), replace = TRUE)

# 4. Duplicate order ids, from a re-run of an export.
dupes <- sample(n, 9)
df <- rbind(df, df[dupes, ])

# 5. Impossible durations, from a clock or a timezone.
df$delivery_days[sample(nrow(df), 6)] <- c(-2, -1, 0, -3, 0, -1)

# 6. Extreme costs, from a currency or a decimal point.
df$cost_gbp[sample(nrow(df), 5)] <- c(9412.00, 8730.55, 12004.10, 7655.20, 10221.75)

df <- df[sample(nrow(df)), ]
write.csv(df, file.path("data", "deliveries.csv"), row.names = FALSE, na = "")
cat("wrote data/deliveries.csv:", nrow(df), "rows\n")
