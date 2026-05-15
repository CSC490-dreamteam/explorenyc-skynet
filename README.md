# Skynet
This codebase is used for creatring and rating synthetic user data for ExploreNYC.

There are 2 main functions: Creating a route and Critiquing it

## Process overview
1. A Random scenario is picked from a list of Scenarios
2. An LLM receives this scenario and a list of places and is told to generate a route to be sent to the routing engine
3. Another LLM validates the JSON from the previous step to ensure it runs properly in the routing engine
4. the trip idea is stored in an SQL table.
5. Any trips that have not yet been materialzied through the routing engine are pulled and materialized, the result is stored back into the SQL table.
6. A trip that has been materialized but not yet rated is grabbed from the DB and passed to an LLM which critiques the trip on various qualities and assigns it a score.
7. The score, explanations for the score and associated trip is stored in the DB.


Steps 1-4 are done in one pass thru 2 LLM calls and steps 5-7 are completed by a separate LLM.
The steps are separated into 2 sections so that they can be done asynchronously over time via CRON jobs.
