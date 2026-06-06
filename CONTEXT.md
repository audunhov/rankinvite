# Context: RankInvite

System for managing incoming invitations to politicians and distributing them according to configurable strategies.

## Glossary

*   **Event** (Arrangement): The external occurrence (e.g., a debate, a meeting) that requires participants.
*   **Spot** (Plass): An available slot for a participant in an Event.
*   **Invitation** (Invitasjon): The top-level object managed by administrators. It represents the process of filling Spots for an Event. It contains one or more **Distribution Strategies**.
*   **Distribution Strategy** (Fordelingsstrategi): A specific logic for offering Spots to people. Multiple strategies can be chained together in an **Invitation**.
*   **Personal Invite** (Personlig invitasjon): A unique offer sent to a specific person via a magic link. It has a status (Pending, Accepted, Declined, TimedOut).

## Strategy Behavior

An Invitation executes its strategies sequentially. A strategy is considered **Finished** when:
1.  **Priority List:** All participants in the list have either Accepted, Declined, or their Personal Invite has TimedOut. (Note: A strategy might finish early if all Spots are filled).
2.  **First-Come-First-Served:** The strategy's total time limit has expired, or all Spots are filled. (Note: A new Personal Invite is sent to all participants in this strategy's list simultaneously when it starts).

### FCFS Logic
*   All participants in the FCFS strategy receive a Personal Invite at the same time.
*   The first participants to click "Accept" fill the available Spots.
*   Once all Spots are filled, any remaining participants clicking their link will see a "Too Late" message.

## Technical Foundation: Deterministic Simulation Testing (DST)

To support DST, the core logic must be a **Pure State Machine**.
*   **Input:** Commands (e.g., `StartInvitation`, `AcceptInvite`, `TickTime`).
*   **State:** The complete data of an Invitation and its progress.
*   **Output:** Events (e.g., `EmailSent`, `SpotFilled`, `StrategyFinished`).
*   **No Side Effects:** The core logic cannot directly send emails or read the current clock. These are provided as inputs or handled by the "shell" around the machine.
