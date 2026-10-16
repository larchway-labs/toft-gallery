# A/B Test — a Shiny example for Toft.
#
# One file, `app.R`, which is what `shiny::runApp` looks for and what Toft
# detects. Nothing here binds a port, reads PORT or builds a URL: the platform
# starts this app by evaluating `shiny::runApp(appDir, host, port)` and serves it
# under /apps/{slug}/. Shiny emits relative asset URLs and derives its WebSocket
# URL from the page's own path, so the prefix takes care of itself.
#
# Only `shiny` is used. Base R already has prop.test, binom.test and
# power.prop.test, which is the whole of the statistics below — a smaller
# renv.lock restores faster on every deploy, and there is nothing here a
# tidyverse dependency would have made clearer.

library(shiny)

app_title <- {
  t <- trimws(Sys.getenv("APP_TITLE", ""))
  if (nzchar(t)) t else "A/B Test"
}

# The decision threshold, as a proportion. Read once at start: an env var is
# configuration, and configuration that changes needs a restart, not a reactive.
alpha <- {
  raw <- suppressWarnings(as.numeric(Sys.getenv("SIGNIFICANCE_LEVEL", "0.05")))
  if (is.na(raw) || raw <= 0 || raw >= 0.5) 0.05 else raw
}

confidence_label <- paste0(format(round(100 * (1 - alpha), 1)), "%")

# ---------------------------------------------------------------------------
# The statistics. Plain functions, no reactivity — everything here is testable
# from the console with Rscript, which is the reason to keep it out of server().
# ---------------------------------------------------------------------------

# compare runs a two-proportion test and returns everything the UI shows.
compare <- function(visitors_a, conversions_a, visitors_b, conversions_b) {
  successes <- c(conversions_a, conversions_b)
  trials <- c(visitors_a, visitors_b)

  # prop.test warns when an expected cell count is small; the warning is the
  # honest answer, so it is captured and shown rather than suppressed.
  warned <- NULL
  test <- withCallingHandlers(
    prop.test(successes, trials, conf.level = 1 - alpha, correct = TRUE),
    warning = function(w) {
      warned <<- conditionMessage(w)
      invokeRestart("muffleWarning")
    }
  )

  rate_a <- conversions_a / visitors_a
  rate_b <- conversions_b / visitors_b

  # Wilson intervals for each arm on its own, which is what the plot draws.
  # prop.test with a single proportion gives the same interval binom.test's
  # exact method would only approximate at these sample sizes.
  arm_interval <- function(x, n) {
    as.numeric(prop.test(x, n, conf.level = 1 - alpha, correct = TRUE)$conf.int)
  }

  list(
    rate_a = rate_a,
    rate_b = rate_b,
    lift = if (rate_a > 0) (rate_b - rate_a) / rate_a else NA_real_,
    difference = rate_b - rate_a,
    # prop.test's interval is p1 - p2; the UI talks about B minus A.
    diff_low = -test$conf.int[2],
    diff_high = -test$conf.int[1],
    p_value = test$p.value,
    ci_a = arm_interval(conversions_a, visitors_a),
    ci_b = arm_interval(conversions_b, visitors_b),
    warning = warned
  )
}

# plan answers the other question people arrive with: how many do I need?
plan <- function(baseline, effects, power, alpha) {
  vapply(effects, function(effect) {
    target <- baseline * (1 + effect)
    if (target <= 0 || target >= 1 || target == baseline) return(NA_real_)
    ceiling(power.prop.test(p1 = baseline, p2 = target,
                            sig.level = alpha, power = power)$n)
  }, numeric(1))
}

# Vectorised, because the plot labels two bars with one call. R 4.2 made
# `if (a_vector_of_two)` an error rather than a warning, so a scalar-only
# formatter used on a pair is a broken plot and a red box on the page.
fmt_pct <- function(x, digits = 2) {
  out <- paste0(formatC(100 * x, format = "f", digits = digits), "%")
  out[is.na(x)] <- "--"
  if (length(x) == 0) return(character(0))
  out
}

fmt_int <- function(x) formatC(x, format = "d", big.mark = ",")

# ---------------------------------------------------------------------------
# UI
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# The house style
#
# `www/` is Shiny's static directory, served under this app's own prefix — so
# the stylesheet is linked rather than pasted into the page, and Shiny handles
# the base path without this file knowing what it is.
#
# The kit is byte-identical in every example in the Toft gallery. What
# follows it is this app's own layer: Bootstrap is what fluidPage draws with,
# and these are the six places it and the kit disagree.
# ---------------------------------------------------------------------------

app_layer <- "
/* fluidPage is a Bootstrap container; the kit expects a page. */

/* The one real collision, and it is worth naming: `.row` is a flex utility in
   the kit and a grid row in Bootstrap, and sidebarLayout is built from one. Left
   alone, the sidebar and the panel stop being side by side. Bootstrap wins
   inside its own container; the kit's utility still works outside it. */
.container-fluid .row { display: block; gap: 0; align-items: initial; flex-wrap: initial; }

body { background: var(--lp-paper); color: var(--lp-ink); font-family: var(--lp-sans); }
.container-fluid { max-width: var(--lp-shell); margin: 0 auto; padding: 0 var(--lp-gutter); }
.lp-full { margin: 0 calc(-1 * var(--lp-gutter)); }

/* Bootstrap's well is Shiny's sidebar. */
.well {
  background: var(--lp-card); border: 1px solid var(--lp-rule);
  border-radius: var(--lp-r); box-shadow: none; padding: 1rem;
}
.well h4 {
  font: 640 .75rem/1.4 var(--lp-sans); letter-spacing: .085em;
  text-transform: uppercase; color: var(--lp-ink-3); margin-bottom: .5rem;
}
.well hr { border-top: 1px solid var(--lp-rule-2); margin: 1rem 0; }
.control-label { font: 560 .75rem/1.4 var(--lp-sans); letter-spacing: .04em; color: var(--lp-ink-2); }
.form-control, .shiny-input-container input[type=number] {
  font: 400 .875rem/1.45 var(--lp-sans); color: var(--lp-ink);
  background: var(--lp-card); border: 1px solid var(--lp-rule);
  border-radius: var(--lp-r-sm); box-shadow: none; height: auto; padding: .4375rem .625rem;
}
.form-control:focus { border-color: var(--lp-primary); box-shadow: none; }
.help-block { font-size: .75rem; color: var(--lp-ink-3); }

/* Bootstrap's tabs, in the kit's idiom. */
.nav-tabs { border-bottom: 1px solid var(--lp-rule); }
.nav-tabs > li > a {
  border: 0; border-bottom: 2px solid transparent; border-radius: 0;
  padding: .875rem .75rem .75rem; margin: 0 .25rem -1px 0;
  font: 560 .875rem/1 var(--lp-sans); color: var(--lp-ink-2); background: none;
}
.nav-tabs > li.active > a, .nav-tabs > li.active > a:hover, .nav-tabs > li.active > a:focus {
  border: 0; border-bottom: 2px solid var(--lp-primary);
  color: var(--lp-primary); font-weight: 620; background: none;
}

/* The verdict: the one thing anybody came here to read. */
.lp-verdict {
  padding: .875rem 1rem; border-radius: var(--lp-r);
  border: 1px solid var(--lp-rule); border-left: 3px solid var(--lp-ink-3);
  background: var(--lp-card); margin-bottom: 1rem; font-size: .9375rem;
}
.lp-yes { border-left-color: var(--lp-ok-solid); background: var(--lp-ok-sur); border-color: var(--lp-ok-brd); }
.lp-no  { border-left-color: var(--lp-neu-solid); }
.lp-note { color: var(--lp-att-fg); font-size: .8125rem; margin-top: .5rem; }

table.lp { width: 100%; border-collapse: collapse; font-size: .875rem; }
table.lp th, table.lp td {
  text-align: left; padding: .5rem .625rem; border-bottom: 1px solid var(--lp-rule-2);
}
table.lp th { color: var(--lp-ink-3); font-weight: 500; width: 42%; }
table.lp td { font-variant-numeric: tabular-nums; }
table.lp tr:last-child th, table.lp tr:last-child td { border-bottom: 0; }
.shiny-plot-output { background: var(--lp-card); border: 1px solid var(--lp-rule); border-radius: var(--lp-r); }
"

# What this example demonstrates, and whether it is really here. Every example
# in the gallery carries one.
rail_cap <- function(label, on, note = "") {
  sprintf('<span class="cap %s" title="%s"><b>%s</b></span>',
          if (isTRUE(on)) "on" else "off", note, label)
}

masthead <- function() {
  slug <- Sys.getenv("TOFT_APP_SLUG", "")
  on_platform <- nzchar(Sys.getenv("TOFT_APP_TOKEN"))
  caps <- paste0(
    rail_cap("Shiny", TRUE,
             "Detected from app.R at the release root. Started by evaluating shiny::runApp with the port as an argument \u2014 R has no --port flag."),
    rail_cap("Keeps its prefix", TRUE,
             "Shiny is one of the two frameworks the proxy forwards the full path to, and it needs the trailing slash."),
    rail_cap("renv.lock", file.exists("renv.lock"),
             "Committed, so the Dependencies tab can enumerate this app."),
    rail_cap("One process", TRUE,
             "One R process serves every viewer, so a long computation is a queue for everybody. Replicas stay at one, which is what makes Shiny deployable here at all."),
    rail_cap("Nothing stored", TRUE,
             "Every number on the page comes from the inputs. No database, no file, no session state that outlives the tab."),
    rail_cap("Toft workload", on_platform,
             if (on_platform) "Running as a Toft workload." else "This is a local run."),
    collapse = "")

  HTML(sprintf('
<div class="lp-full">
<div class="masthead"><div class="masthead-in">
<div>
<div class="wordmark"><span class="mark"></span><span class="wordmark-text">Toft example</span></div>
<h1>%s</h1>
<p class="standfirst">Two variants&rsquo; visitors and conversions in, and a difference,
a confidence interval and a p-value out. Nothing is stored.</p>
</div>
<div class="masthead-aside">
<span class="chip chip-lang">R &middot; Shiny</span>
<span class="chip">%s confidence</span>
</div>
</div></div>
<div class="rail"><div class="rail-in"><span class="rail-label">Toft</span>%s</div></div>
</div>', app_title, confidence_label, caps))
}

ui <- fluidPage(
  title = app_title,
  tags$head(
    tags$link(rel = "stylesheet", type = "text/css", href = "toft-kit.css"),
    tags$link(rel = "icon", type = "image/svg+xml", href = "favicon.svg"),
    tags$style(HTML(app_layer))
  ),

  masthead(),
  div(style = "height:1.5rem"),

  tabsetPanel(
    tabPanel(
      "Compare",
      br(),
      sidebarLayout(
        sidebarPanel(
          width = 4,
          h4("A (control)"),
          numericInput("visitors_a", "Visitors", value = 4820, min = 1, step = 1),
          numericInput("conversions_a", "Conversions", value = 241, min = 0, step = 1),
          tags$hr(),
          h4("B (variant)"),
          numericInput("visitors_b", "Visitors", value = 4771, min = 1, step = 1),
          numericInput("conversions_b", "Conversions", value = 289, min = 0, step = 1)
        ),
        mainPanel(
          width = 8,
          uiOutput("verdict"),
          plotOutput("rates", height = "260px"),
          br(),
          uiOutput("detail")
        )
      )
    ),
    tabPanel(
      "Plan",
      br(),
      sidebarLayout(
        sidebarPanel(
          width = 4,
          numericInput("baseline", "Baseline conversion rate (%)", value = 5, min = 0.01, max = 99, step = 0.1),
          sliderInput("power", "Power", value = 0.8, min = 0.5, max = 0.99, step = 0.01),
          helpText("Sample size is per arm. Double it for the test as a whole.")
        ),
        mainPanel(
          width = 8,
          plotOutput("planplot", height = "300px"),
          br(),
          uiOutput("plantable")
        )
      )
    )
  ),

  HTML('
<div class="lp-full" style="margin-top:2rem">
<div class="foot"><div class="foot-in">
<span>An example app from the <strong>Toft</strong> gallery.</span>
<span>R &middot; Shiny &middot; base statistics &middot; base graphics</span>
</div></div>
</div>')
)

# ---------------------------------------------------------------------------
# Server
#
# Every computation here is microseconds. That is not an accident and it is the
# thing to copy: Toft runs one R process per Shiny app, so a slow reactive
# is a queue for every viewer at once, not just the one who triggered it.
# ---------------------------------------------------------------------------

server <- function(input, output, session) {

  valid <- reactive({
    v <- c(input$visitors_a, input$conversions_a, input$visitors_b, input$conversions_b)
    all(!is.na(v)) && all(v >= 0) &&
      input$visitors_a > 0 && input$visitors_b > 0 &&
      input$conversions_a <= input$visitors_a &&
      input$conversions_b <= input$visitors_b
  })

  result <- reactive({
    req(valid())
    compare(input$visitors_a, input$conversions_a, input$visitors_b, input$conversions_b)
  })

  output$verdict <- renderUI({
    if (!valid()) {
      return(div(class = "lp-verdict lp-no",
                 "Conversions cannot exceed visitors, and visitors must be at least one."))
    }
    r <- result()
    # Both arms at 0% (or both at 100%) leaves prop.test with no variance and a
    # p-value of NaN. Left alone that reaches `if (significant)` as NA and takes
    # the session down as "Disconnected from server", which is the one failure
    # mode a Shiny app must not have: the viewer is told nothing and the log
    # says the app is fine.
    if (!is.finite(r$p_value)) {
      return(div(class = "lp-verdict lp-no",
                 strong("Nothing to compare."),
                 sprintf(" Both variants converted at %s, so there is no difference to test.",
                         fmt_pct(r$rate_a))))
    }
    significant <- r$p_value < alpha
    direction <- if (r$difference >= 0) "better" else "worse"
    tagList(
      div(class = paste("lp-verdict", if (significant) "lp-yes" else "lp-no"),
          if (significant) {
            tagList(strong("A real difference."), sprintf(
              " B is %s than A by %s (%s of A's rate), and the test rules out no difference at p = %s.",
              direction, fmt_pct(abs(r$difference)), fmt_pct(abs(r$lift), 1),
              formatC(r$p_value, format = "g", digits = 3)))
          } else {
            tagList(strong("Not conclusive."), sprintf(
              " B measures %s than A by %s, but at p = %s that is inside what chance produces at this sample size.",
              direction, fmt_pct(abs(r$difference)),
              formatC(r$p_value, format = "g", digits = 3)))
          },
          if (!is.null(r$warning)) div(class = "lp-note",
            "The test is approximate here: ", r$warning)
      )
    )
  })

  output$rates <- renderPlot({
    req(valid())
    r <- result()
    rates <- c(r$rate_a, r$rate_b)
    lows <- c(r$ci_a[1], r$ci_b[1])
    highs <- c(r$ci_a[2], r$ci_b[2])
    top <- max(highs) * 1.15

    op <- par(mar = c(3.5, 4.5, 1, 1), bg = "white", family = "sans",
              col.axis = "#5A5F6B", col.lab = "#5A5F6B", fg = "#E5E6EA")
    on.exit(par(op))
    centres <- barplot(rates * 100, names.arg = c("A (control)", "B (variant)"),
                       col = c("#8A8F9B", "#4338CA"), border = NA,
                       ylim = c(0, top * 100), ylab = "Conversion rate (%)",
                       cex.names = 1.05, las = 1)
    arrows(centres, lows * 100, centres, highs * 100,
           angle = 90, code = 3, length = 0.08, col = "#14161C", lwd = 2)
    text(centres, rates * 100, labels = fmt_pct(rates), pos = 3, offset = 1.6, col = "#14161C")
  })

  output$detail <- renderUI({
    req(valid())
    r <- result()
    rows <- list(
      c("Rate A", fmt_pct(r$rate_a)),
      c("Rate B", fmt_pct(r$rate_b)),
      c("Absolute difference (B - A)", fmt_pct(r$difference)),
      c(paste0(confidence_label, " interval for the difference"),
        paste0(fmt_pct(r$diff_low), " to ", fmt_pct(r$diff_high))),
      c("Relative lift", fmt_pct(r$lift, 1)),
      c("p-value", formatC(r$p_value, format = "g", digits = 4))
    )
    tags$table(class = "toft",
      lapply(rows, function(row) tags$tr(tags$th(row[1]), tags$td(row[2]))))
  })

  effects <- c(0.05, 0.10, 0.15, 0.20, 0.30, 0.50)

  sizes <- reactive({
    baseline <- input$baseline / 100
    req(!is.na(baseline), baseline > 0, baseline < 1)
    plan(baseline, effects, input$power, alpha)
  })

  output$planplot <- renderPlot({
    n <- sizes()
    req(all(!is.na(n)))
    op <- par(mar = c(4.5, 5.5, 1, 1), bg = "white", family = "sans",
              col.axis = "#5A5F6B", col.lab = "#5A5F6B", fg = "#E5E6EA")
    on.exit(par(op))
    plot(effects * 100, n, type = "b", pch = 19, col = "#4338CA", lwd = 2,
         log = "y", las = 1, xlab = "Relative lift you want to detect (%)",
         ylab = "Visitors per arm", panel.first = grid(col = "#E5E6EA", lty = 1))
    text(effects * 100, n, labels = fmt_int(n), pos = 3, cex = 0.85, col = "#14161C")
  })

  output$plantable <- renderUI({
    n <- sizes()
    req(all(!is.na(n)))
    tags$table(class = "toft",
      tags$tr(tags$th("Relative lift"), tags$td("Visitors per arm")),
      Map(function(e, v) tags$tr(tags$th(fmt_pct(e, 0)), tags$td(fmt_int(v))),
          effects, n))
  })
}

shinyApp(ui, server)
